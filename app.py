#!/usr/bin/env python3.12
from __future__ import annotations

import asyncio
import json
import logging
import math
import random
import re
import unicodedata
from dataclasses import dataclass, field
from datetime import datetime, timezone
from io import BytesIO
from typing import Any, Dict, List, Optional, Sequence, Tuple

import aiosqlite
import qrcode
from barcode import Code128
from barcode.writer import ImageWriter
from zoneinfo import ZoneInfo

from telegram import Message, MessageEntity, Update
from telegram.constants import ChatType, ParseMode, MessageEntityType
from telegram.error import BadRequest, Forbidden
from telegram.ext import (
    Application,
    ContextTypes,
    MessageHandler,
    filters,
)

# -----------------------------
# Logging
# -----------------------------
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)
logger = logging.getLogger("tg-bot")

# -----------------------------
# Constants / Types
# -----------------------------
class FileType(str):
    pass


FILE_PHOTO = FileType("photo")
FILE_GIF = FileType("gif")
FILE_STICKER = FileType("sticker")
FILE_VOICE = FileType("voice")
FILE_VIDEO = FileType("video")
FILE_DOCUMENT = FileType("document")
FILE_VIDEONOTE = FileType("videonote")
FILE_AUDIO = FileType("audio")


@dataclass
class MyResponse:
    id: int = 0
    search_phrase: str = ""
    response: str = ""
    file_type: FileType = FileType("")
    file_id: str = ""
    file_name: str = ""
    entities: List[MessageEntity] = field(default_factory=list)


BOT_TOKEN_PATH = "./config/token.txt"
DB_PATH = "./mydb.db"

ADMIN_ID = 193117018  # used for /addglobal /removeglobal /getlink
BLOCKED_COMMAND_USER_ID = 89886125

# -----------------------------
# Keyword cooldown (5 minutes)
# -----------------------------
_last_keyword_timestamps: Dict[str, datetime] = {}
_last_keyword_lock = asyncio.Lock()


def _cooldown_key(chat_id: int, keyword: str) -> str:
    return f"{chat_id}:{keyword}"


async def check_and_update_last_keyword(chat_id: int, keyword: str) -> bool:
    now = datetime.now(timezone.utc)
    key = _cooldown_key(chat_id, keyword)
    async with _last_keyword_lock:
        last = _last_keyword_timestamps.get(key)
        if last is not None and (now - last).total_seconds() < 5 * 60:
            return False
        _last_keyword_timestamps[key] = now
        return True


# -----------------------------
# Utilities
# -----------------------------
def read_bot_token(path: str) -> str:
    with open(path, "r", encoding="utf-8") as f:
        return f.readline().strip()


def nfc_casefold(s: str) -> str:
    return unicodedata.normalize("NFC", s).casefold()


def norm_key(s: Optional[str]) -> str:
    """Stable normalization for trigger keys (Unicode + casefold + trim)."""
    return nfc_casefold((s or "").strip())


def message_matches(message_text: str, target: str) -> bool:
    return nfc_casefold(message_text) == nfc_casefold(target)


def message_contains(message_text: str, target: str) -> bool:
    return nfc_casefold(target) in nfc_casefold(message_text)


def sanitize_filename(name: str) -> str:
    return re.sub(r"[^a-zA-Z0-9.\-]", "_", name)


def parse_command_and_args(message: Message, bot_username: Optional[str]) -> Tuple[Optional[str], str]:
    """
    Parse /command [args...] from message.text using bot_command entity.
    Strips optional @BotName suffix.
    """
    if not message.text or not message.entities:
        return None, ""

    first = message.entities[0]
    if first.type != MessageEntityType.BOT_COMMAND or first.offset != 0:
        return None, ""

    cmd_token = message.text[: first.length]  # like "/add" or "/add@MyBot"
    cmd = cmd_token[1:]
    if "@" in cmd and bot_username:
        base, at = cmd.split("@", 1)
        if at.casefold() == bot_username.casefold():
            cmd = base
        else:
            return None, ""

    args = message.text[first.length :].lstrip()
    return cmd.casefold(), args


# -----------------------------
# Optional: entity-to-markdown formatter (ported; not used by default)
# -----------------------------
def _utf16_code_unit_offsets(text: str) -> List[int]:
    offsets: List[int] = []
    cu = 0
    for ch in text:
        offsets.append(cu)
        cu += 2 if ord(ch) > 0xFFFF else 1
    return offsets


def _codeunit_to_index(mapping: List[int], codeunit_offset: int) -> int:
    for i, off in enumerate(mapping):
        if off >= codeunit_offset:
            return i
    return len(mapping)


def apply_entities_to_text_markdown(text: str, entities: Sequence[MessageEntity]) -> str:
    runes = list(text)
    mapping = _utf16_code_unit_offsets(text)

    enriched = []
    for e in entities:
        start = _codeunit_to_index(mapping, e.offset)
        end = _codeunit_to_index(mapping, e.offset + e.length)
        enriched.append((start, end, e))

    enriched.sort(key=lambda x: x[0], reverse=True)

    for start, end, ent in enriched:
        before = "".join(runes[:start])
        middle = "".join(runes[start:end])
        after = "".join(runes[end:])
        t = ent.type
        if t == "bold":
            middle = f"**{middle}**"
        elif t == "italic":
            middle = f"*{middle}*"
        elif t == "code":
            middle = f"`{middle}`"
        elif t == "pre":
            middle = f"```{middle}```"
        elif t == "url" and getattr(ent, "url", None):
            middle = f"[{middle}]({ent.url})"
        new_text = before + middle + after
        runes = list(new_text)
        mapping = _utf16_code_unit_offsets(new_text)

    return "".join(runes)


# -----------------------------
# DB Layer (aiosqlite)
# -----------------------------
class Database:
    def __init__(self, path: str) -> None:
        self.path = path
        self.conn: Optional[aiosqlite.Connection] = None
        self.lock = asyncio.Lock()

    async def connect(self) -> None:
        self.conn = await aiosqlite.connect(self.path)
        self.conn.row_factory = aiosqlite.Row
        async with self.conn.execute("PRAGMA foreign_keys = ON;"):
            pass
        await self.conn.commit()

    async def close(self) -> None:
        if self.conn is not None:
            await self.conn.close()
            self.conn = None

    async def execute(self, sql: str, params: Sequence[Any] = ()) -> Tuple[int, int]:
        """
        Executes SQL and commits.
        Returns (rowcount, lastrowid). Always closes cursor.
        """
        assert self.conn is not None
        async with self.lock:
            async with self.conn.execute(sql, params) as cur:
                await self.conn.commit()
                rowcount = int(cur.rowcount) if cur.rowcount is not None else 0
                lastrowid = int(cur.lastrowid) if cur.lastrowid is not None else 0
                return rowcount, lastrowid

    async def fetchone(self, sql: str, params: Sequence[Any] = ()) -> Optional[aiosqlite.Row]:
        assert self.conn is not None
        async with self.lock:
            async with self.conn.execute(sql, params) as cur:
                return await cur.fetchone()

    async def fetchall(self, sql: str, params: Sequence[Any] = ()) -> List[aiosqlite.Row]:
        assert self.conn is not None
        async with self.lock:
            async with self.conn.execute(sql, params) as cur:
                return await cur.fetchall()

    async def init_schema(self) -> None:
        await self.execute(
            "CREATE TABLE IF NOT EXISTS timezones (chatID INTEGER, location TEXT, PRIMARY KEY (chatID, location))"
        )
        await self.execute("CREATE TABLE IF NOT EXISTS messagelist (chatID INTEGER PRIMARY KEY, messageID INTEGER)")
        await self.execute("CREATE TABLE IF NOT EXISTS alias (chatID INTEGER, location TEXT, alias TEXT)")

        await self.execute(
            """
            CREATE TABLE IF NOT EXISTS triggers (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                chat_id INTEGER,
                search_phrase TEXT,
                response TEXT,
                file_type TEXT,
                file_id TEXT,
                file_name TEXT,
                is_global BOOLEAN,
                entities TEXT
            )
            """
        )

        # legacy cascade table (kept)
        await self.execute(
            """
            CREATE TABLE IF NOT EXISTS cascade_triggers (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                chat_id INTEGER,
                search_phrase TEXT,
                responses TEXT
            )
            """
        )

        await self.execute(
            """
            CREATE TABLE IF NOT EXISTS cascade_triggers2 (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                chat_id INTEGER NOT NULL,
                search_phrase TEXT NOT NULL,
                UNIQUE(chat_id, search_phrase)
            )
            """
        )
        await self.execute(
            """
            CREATE TABLE IF NOT EXISTS cascade_trigger_responses (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                cascade_trigger_id INTEGER NOT NULL,
                response TEXT,
                file_type TEXT,
                file_id TEXT,
                file_name TEXT,
                entities TEXT,
                FOREIGN KEY(cascade_trigger_id) REFERENCES cascade_triggers2(id) ON DELETE CASCADE
            )
            """
        )

        await self.execute(
            """
            CREATE TABLE IF NOT EXISTS terpet_count (
                user_id INTEGER PRIMARY KEY,
                username TEXT,
                first_name TEXT,
                count INTEGER DEFAULT 0
            )
            """
        )

        # Indexes for fast lookup
        await self.execute("CREATE INDEX IF NOT EXISTS idx_triggers_local ON triggers(chat_id, is_global, search_phrase)")
        await self.execute("CREATE INDEX IF NOT EXISTS idx_triggers_global ON triggers(is_global, search_phrase)")
        await self.execute("CREATE INDEX IF NOT EXISTS idx_cascade_phrase ON cascade_triggers2(chat_id, search_phrase)")
        await self.execute("CREATE INDEX IF NOT EXISTS idx_cascade_resp ON cascade_trigger_responses(cascade_trigger_id)")

        # FIX #5: prevent future duplicates in triggers
        # If your migration already removed duplicates, this should succeed.
        try:
            await self.execute(
                "CREATE UNIQUE INDEX IF NOT EXISTS idx_triggers_unique ON triggers(chat_id, is_global, search_phrase)"
            )
        except Exception as e:
            logger.warning("Failed to create triggers UNIQUE index (duplicates still exist?): %s", e)

    async def get_all_active_chat_ids(self) -> List[int]:
        rows = await self.fetchall("SELECT DISTINCT chatID FROM timezones")
        return [int(r["chatID"]) for r in rows]


# -----------------------------
# Timezone helpers (ported)
# -----------------------------
def get_current_time_for_location(location: str) -> datetime:
    prefixes = ["Europe/", "America/", "Asia/", "Africa/", "Australia/"]

    parts = [p for p in location.strip().split() if p]
    normalized = "_".join([p[:1].upper() + p[1:].lower() if p else p for p in parts]) or location.strip()
    normalized = normalized.replace(" ", "_")

    try:
        tz = ZoneInfo(normalized)
        return datetime.now(tz)
    except Exception:
        pass

    last_exc: Optional[Exception] = None
    for pref in prefixes:
        try:
            tz = ZoneInfo(pref + normalized)
            return datetime.now(tz)
        except Exception as e:
            last_exc = e

    raise last_exc or ValueError("Invalid location")


def is_time_between(dt: datetime, hour1: int, hour2: int) -> bool:
    h = dt.hour
    return hour1 <= h <= hour2


def is_time_between_19_and_8(dt: datetime) -> bool:
    h = dt.hour
    return h >= 19 or h <= 8


# -----------------------------
# Trigger helpers
# -----------------------------
async def check_trigger_existence(db: Database, chat_id: int, search_phrase: str) -> Tuple[bool, bool]:
    key = norm_key(search_phrase)

    row1 = await db.fetchone(
        "SELECT 1 AS x FROM triggers WHERE chat_id = ? AND is_global = ? AND search_phrase = ? LIMIT 1",
        (chat_id, False, key),
    )
    row2 = await db.fetchone(
        "SELECT 1 AS x FROM cascade_triggers2 WHERE chat_id = ? AND search_phrase = ? LIMIT 1",
        (chat_id, key),
    )
    return (row1 is not None), (row2 is not None)


def allowed_message_type(reply_msg: Message) -> bool:
    return reply_msg.game is None


def extract_reply_entities(reply_msg: Message) -> List[MessageEntity]:
    # custom emoji entities are NOT filtered (per your request)
    if reply_msg.entities:
        return list(reply_msg.entities)
    if reply_msg.caption_entities:
        return list(reply_msg.caption_entities)
    return []


def myresponse_has_content(mr: MyResponse) -> bool:
    if (mr.response or "").strip():
        return True
    if (mr.file_id or "").strip():
        return True
    if (mr.file_type or "").strip():
        return True
    return False


def create_my_response_from_reply(message: Message) -> MyResponse:
    assert message.reply_to_message is not None
    r = message.reply_to_message

    if not allowed_message_type(r):
        raise ValueError("Unsupported message type (game)")

    response_text = (r.caption or "").strip() or (r.text or "").strip()
    entities = extract_reply_entities(r)
    mr = MyResponse(response=response_text, entities=entities)

    if r.photo:
        mr.file_type = FILE_PHOTO
        mr.file_id = r.photo[-1].file_id
    elif r.animation:
        mr.file_type = FILE_GIF
        mr.file_id = r.animation.file_id
    elif r.voice:
        mr.file_type = FILE_VOICE
        mr.file_id = r.voice.file_id
    elif r.sticker:
        mr.file_type = FILE_STICKER
        mr.file_id = r.sticker.file_id
    elif r.video:
        mr.file_type = FILE_VIDEO
        mr.file_id = r.video.file_id
    elif r.document:
        mr.file_type = FILE_DOCUMENT
        mr.file_id = r.document.file_id
        mr.file_name = r.document.file_name or ""
    elif r.audio:
        mr.file_type = FILE_AUDIO
        mr.file_id = r.audio.file_id
        mr.file_name = r.audio.file_name or ""
    elif r.video_note:
        mr.file_type = FILE_VIDEONOTE
        mr.file_id = r.video_note.file_id

    return mr


def entities_to_json(entities: Sequence[MessageEntity]) -> str:
    if not entities:
        return ""
    return json.dumps([e.to_dict() for e in entities], ensure_ascii=False)


def entities_from_json(s: str) -> List[MessageEntity]:
    if not s:
        return []
    try:
        data = json.loads(s)
        out: List[MessageEntity] = []
        for d in data:
            out.append(MessageEntity(**d))
        return out
    except Exception:
        return []


# -----------------------------
# Sending responses
# -----------------------------
async def send_trigger_response(
    context: ContextTypes.DEFAULT_TYPE,
    message: Message,
    resp: MyResponse,
    reply_to: bool,
) -> None:
    bot = context.bot
    chat_id = message.chat_id
    reply_to_message_id = message.message_id if reply_to else None
    text = resp.response or ""

    ft = resp.file_type or FileType("")
    if ft == FILE_PHOTO:
        await bot.send_photo(
            chat_id=chat_id,
            photo=resp.file_id,
            caption=text or None,
            caption_entities=resp.entities or None,
            reply_to_message_id=reply_to_message_id,
            message_thread_id=message.message_thread_id,
        )
    elif ft == FILE_GIF:
        await bot.send_animation(
            chat_id=chat_id,
            animation=resp.file_id,
            caption=text or None,
            caption_entities=resp.entities or None,
            reply_to_message_id=reply_to_message_id,
            message_thread_id=message.message_thread_id,
        )
    elif ft == FILE_VOICE:
        await bot.send_voice(
            chat_id=chat_id,
            voice=resp.file_id,
            reply_to_message_id=reply_to_message_id,
            message_thread_id=message.message_thread_id,
        )
    elif ft == FILE_STICKER:
        await bot.send_sticker(
            chat_id=chat_id,
            sticker=resp.file_id,
            reply_to_message_id=reply_to_message_id,
            message_thread_id=message.message_thread_id,
        )
    elif ft == FILE_VIDEO:
        await bot.send_video(
            chat_id=chat_id,
            video=resp.file_id,
            caption=text or None,
            caption_entities=resp.entities or None,
            reply_to_message_id=reply_to_message_id,
            message_thread_id=message.message_thread_id,
        )
    elif ft == FILE_DOCUMENT:
        await bot.send_document(
            chat_id=chat_id,
            document=resp.file_id,
            caption=text or None,
            caption_entities=resp.entities or None,
            reply_to_message_id=reply_to_message_id,
            message_thread_id=message.message_thread_id,
        )
    elif ft == FILE_VIDEONOTE:
        await bot.send_video_note(
            chat_id=chat_id,
            video_note=resp.file_id,
            reply_to_message_id=reply_to_message_id,
            message_thread_id=message.message_thread_id,
        )
    elif ft == FILE_AUDIO:
        await bot.send_audio(
            chat_id=chat_id,
            audio=resp.file_id,
            caption=text or None,
            caption_entities=resp.entities or None,
            reply_to_message_id=reply_to_message_id,
            message_thread_id=message.message_thread_id,
        )
    else:
        if not text.strip():
            return
        await bot.send_message(
            chat_id=chat_id,
            text=text,
            entities=resp.entities or None,
            reply_to_message_id=reply_to_message_id,
            message_thread_id=message.message_thread_id,
        )


# -----------------------------
# Small helper: chunk long messages (Telegram limit ~4096)
# -----------------------------
async def send_long_text(
    context: ContextTypes.DEFAULT_TYPE,
    chat_id: int,
    text: str,
    message_thread_id: Optional[int] = None,
) -> None:
    limit = 3800
    s = text or ""
    while s:
        if len(s) <= limit:
            await context.bot.send_message(chat_id=chat_id, text=s, message_thread_id=message_thread_id)
            return

        cut = s.rfind("\n", 0, limit)
        if cut <= 0:
            cut = limit
        chunk = s[:cut].rstrip()
        s = s[cut:].lstrip("\n")

        if chunk:
            await context.bot.send_message(chat_id=chat_id, text=chunk, message_thread_id=message_thread_id)


# -----------------------------
# Commands
# -----------------------------
async def handle_chatid(context: ContextTypes.DEFAULT_TYPE, message: Message) -> None:
    await context.bot.send_message(
        chat_id=message.chat_id,
        text=f"This chat ID is: {message.chat_id}",
        message_thread_id=message.message_thread_id,
    )


async def handle_getlink(context: ContextTypes.DEFAULT_TYPE, message: Message, args: str) -> None:
    if not message.from_user or message.from_user.id != ADMIN_ID:
        return
    user_id = args.strip()
    if not user_id:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Please provide an ID.",
            message_thread_id=message.message_thread_id,
        )
        return
    link = f"<a href='tg://user?id={user_id}'>Link to User</a>"
    await context.bot.send_message(
        chat_id=message.chat_id,
        text=link,
        parse_mode=ParseMode.HTML,
        disable_web_page_preview=True,
        message_thread_id=message.message_thread_id,
    )


async def handle_roll(context: ContextTypes.DEFAULT_TYPE, message: Message, sides: int) -> None:
    n = random.randint(1, sides)
    await context.bot.send_message(
        chat_id=message.chat_id,
        text=f"🎲 You rolled: {n}",
        reply_to_message_id=message.message_id,
        message_thread_id=message.message_thread_id,
    )


async def handle_generate_qr(context: ContextTypes.DEFAULT_TYPE, message: Message, args: str) -> None:
    code = args.strip()
    if not code:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Usage: /generateqr <text>",
            reply_to_message_id=message.message_id,
            message_thread_id=message.message_thread_id,
        )
        return
    img = qrcode.make(code)
    bio = BytesIO()
    bio.name = f"{sanitize_filename(code)[:40]}.png"
    img.save(bio, format="PNG")
    bio.seek(0)
    await context.bot.send_photo(
        chat_id=message.chat_id,
        photo=bio,
        reply_to_message_id=message.message_id,
        message_thread_id=message.message_thread_id,
    )


async def handle_generate_barcode(context: ContextTypes.DEFAULT_TYPE, message: Message, args: str) -> None:
    code = args.strip()
    if not code:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Usage: /generatebar <text>",
            reply_to_message_id=message.message_id,
            message_thread_id=message.message_thread_id,
        )
        return
    barcode_obj = Code128(code, writer=ImageWriter())
    bio = BytesIO()
    bio.name = f"{sanitize_filename(code)[:40]}.png"
    barcode_obj.write(bio, options={"write_text": False})
    bio.seek(0)
    await context.bot.send_photo(
        chat_id=message.chat_id,
        photo=bio,
        reply_to_message_id=message.message_id,
        message_thread_id=message.message_thread_id,
    )


# ---- Sample size ----
@dataclass
class SampleSize:
    low: int
    medium: int
    high: int


def get_risk_size(sizes: SampleSize, risk: str) -> int:
    if risk == "low":
        return sizes.low
    if risk == "medium":
        return sizes.medium
    if risk == "high":
        return sizes.high
    return 0


def interpolate(x0: float, y0: float, x: float, x1: float, y1: float) -> float:
    if x1 == x0:
        return y0
    return y0 + (y1 - y0) * (x - x0) / (x1 - x0)


def get_sample_size(population: int, risk: str) -> int:
    rows = [
        (0, 1, SampleSize(1, 1, 1)),
        (2, 4, SampleSize(2, 2, 2)),
        (5, 12, SampleSize(2, 3, 5)),
        (13, 52, SampleSize(5, 10, 15)),
        (53, 250, SampleSize(20, 30, 40)),
        (251, 2**31 - 1, SampleSize(25, 45, 60)),
    ]

    if population > 250:
        return get_risk_size(rows[-1][2], risk)

    for i, (_minp, maxp, sizes) in enumerate(rows):
        if population <= maxp:
            if i == 0 or population == maxp:
                return get_risk_size(sizes, risk)
            prev = rows[i - 1]
            y0 = float(get_risk_size(prev[2], risk))
            y1 = float(get_risk_size(sizes, risk))
            val = interpolate(float(prev[1]), y0, float(population), float(maxp), y1)
            return int(math.ceil(val))

    raise ValueError("population out of range")


def generate_random_selection(sample_size: int, population: int) -> List[int]:
    if sample_size > population:
        return []
    s = set()
    while len(s) < sample_size:
        s.add(random.randint(1, population))
    return sorted(s)


async def handle_samplesize(context: ContextTypes.DEFAULT_TYPE, message: Message) -> None:
    parts = (message.text or "").split()
    if len(parts) < 3:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Usage: /samplesize <low|medium|high> <population>",
            message_thread_id=message.message_thread_id,
        )
        return
    risk = parts[1].casefold()
    try:
        population = int(parts[2])
    except ValueError:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text=f"Invalid population number: {parts[2]}",
            message_thread_id=message.message_thread_id,
        )
        return

    try:
        ss = get_sample_size(population, risk)
        if ss <= 0:
            raise ValueError("Risk must be one of: low, medium, high")
    except Exception as e:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text=str(e),
            message_thread_id=message.message_thread_id,
        )
        return

    selection = generate_random_selection(ss, population)
    out = f"For {risk} risk and population of {population}, sample size is {ss}\n"
    out += "Random numbers for random selection:\n" + "\n".join(map(str, selection))
    await context.bot.send_message(chat_id=message.chat_id, text=out, message_thread_id=message.message_thread_id)


# ---- Terpet ----
def get_raz_form(count: int) -> str:
    if count % 10 == 1 and count % 100 != 11:
        return "раз"
    if 2 <= (count % 10) <= 4 and not (12 <= (count % 100) <= 14):
        return "раза"
    return "раз"


async def handle_terpet_message(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message) -> None:
    if message.chat_id == -1001390115843 and ((message.text or "") == "Терпеть" or (message.text or "") == "/terpet"):
        u = message.from_user
        if not u:
            return
        await db.execute(
            """
            INSERT INTO terpet_count (user_id, username, first_name, count)
            VALUES (?, ?, ?, 1)
            ON CONFLICT(user_id) DO UPDATE SET count = count + 1, username = excluded.username, first_name = excluded.first_name
            """,
            (u.id, u.username or "", u.first_name or ""),
        )
        row = await db.fetchone("SELECT count FROM terpet_count WHERE user_id = ?", (u.id,))
        count = int(row["count"]) if row else 1
        await context.bot.send_message(
            chat_id=message.chat_id,
            text=f"Вы терпели {count} {get_raz_form(count)}",
            reply_to_message_id=message.message_id,
            message_thread_id=message.message_thread_id,
        )


async def handle_topterpil(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message) -> None:
    rows = await db.fetchall(
        """
        SELECT COALESCE(NULLIF(username, ''), first_name) AS name, count
        FROM terpet_count
        ORDER BY count DESC
        LIMIT 5
        """
    )
    if not rows:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="No terpet data available.",
            message_thread_id=message.message_thread_id,
        )
        return
    lines = []
    for r in rows:
        name = r["name"] or "?"
        count = int(r["count"])
        lines.append(f"{name}: {count} {get_raz_form(count)}")
    await context.bot.send_message(
        chat_id=message.chat_id,
        text="Топ-5 Терпил:\n" + "\n".join(lines),
        message_thread_id=message.message_thread_id,
    )


# ---- Triggers ----
async def handle_triggers_command(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message) -> None:
    local = await db.fetchall(
        "SELECT DISTINCT search_phrase FROM triggers WHERE chat_id = ? AND is_global = ? ORDER BY search_phrase",
        (message.chat_id, False),
    )
    glob = await db.fetchall(
        "SELECT DISTINCT search_phrase FROM triggers WHERE is_global = ? ORDER BY search_phrase",
        (True,),
    )
    casc = await db.fetchall(
        "SELECT DISTINCT search_phrase FROM cascade_triggers2 WHERE chat_id = ? ORDER BY search_phrase",
        (message.chat_id,),
    )

    lines: List[str] = []
    lines.append("Local Triggers:")
    if local:
        for r in local:
            lines.append(f"- {r['search_phrase']}")
    else:
        lines.append("- (none)")

    lines.append("")
    lines.append("Global Triggers:")
    if glob:
        for r in glob:
            lines.append(f"- {r['search_phrase']}")
    else:
        lines.append("- (none)")

    lines.append("")
    lines.append("Cascade Triggers:")
    if casc:
        for r in casc:
            lines.append(f"- {r['search_phrase']}")
    else:
        lines.append("- (none)")

    await send_long_text(
        context=context,
        chat_id=message.chat_id,
        text="\n".join(lines),
        message_thread_id=message.message_thread_id,
    )


async def handle_remove(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message, args: str) -> None:
    if message.from_user and message.from_user.id == BLOCKED_COMMAND_USER_ID:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="You are not allowed to use this command.",
            message_thread_id=message.message_thread_id,
        )
        return

    phrase = norm_key(args)
    deleted, _ = await db.execute(
        "DELETE FROM triggers WHERE chat_id = ? AND search_phrase = ? AND is_global = ?",
        (message.chat_id, phrase, False),
    )
    if deleted > 0:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Local response removed!",
            message_thread_id=message.message_thread_id,
        )
    else:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="No local response found with that search phrase.",
            message_thread_id=message.message_thread_id,
        )


async def handle_removeglobal(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message, args: str) -> None:
    if not message.from_user or message.from_user.id != ADMIN_ID:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="You are not authorized to use this command.",
            message_thread_id=message.message_thread_id,
        )
        return
    phrase = norm_key(args)
    deleted, _ = await db.execute(
        "DELETE FROM triggers WHERE search_phrase = ? AND is_global = ?",
        (phrase, True),
    )
    if deleted > 0:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Global response removed!",
            message_thread_id=message.message_thread_id,
        )
    else:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="No global response found with that search phrase.",
            message_thread_id=message.message_thread_id,
        )


async def handle_add(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message, args: str) -> None:
    if message.from_user and message.from_user.id == BLOCKED_COMMAND_USER_ID:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="You are not allowed to use this command.",
            message_thread_id=message.message_thread_id,
        )
        return

    if not message.reply_to_message:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Please reply to a message and use: /add <phrase>",
            message_thread_id=message.message_thread_id,
        )
        return

    phrase_raw = args.strip()
    if not phrase_raw:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Please provide a trigger phrase.\nExample: /add Hello",
            message_thread_id=message.message_thread_id,
        )
        return
    phrase = norm_key(phrase_raw)

    _, cascade_exists = await check_trigger_existence(db, message.chat_id, phrase)
    if cascade_exists:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="A cascade trigger with this phrase already exists. Cannot create a normal trigger.",
            message_thread_id=message.message_thread_id,
        )
        return

    exists = await db.fetchone(
        "SELECT id FROM triggers WHERE chat_id = ? AND is_global = ? AND search_phrase = ? LIMIT 1",
        (message.chat_id, False, phrase),
    )

    try:
        mr = create_my_response_from_reply(message)
    except Exception:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Can't add this trigger",
            message_thread_id=message.message_thread_id,
        )
        return

    if not myresponse_has_content(mr):
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Can't add this trigger: replied message has no text/caption and no supported media.",
            message_thread_id=message.message_thread_id,
        )
        return

    mr.search_phrase = phrase
    ent_json = entities_to_json(mr.entities)

    if exists:
        await db.execute(
            """
            UPDATE triggers
            SET response = ?, file_type = ?, file_id = ?, file_name = ?, entities = ?
            WHERE id = ?
            """,
            (mr.response, str(mr.file_type), mr.file_id, mr.file_name, ent_json, int(exists["id"])),
        )
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Response updated!",
            message_thread_id=message.message_thread_id,
        )
        return

    await db.execute(
        """
        INSERT INTO triggers (chat_id, search_phrase, response, file_type, file_id, file_name, entities, is_global)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        """,
        (message.chat_id, phrase, mr.response, str(mr.file_type), mr.file_id, mr.file_name, ent_json, False),
    )
    await context.bot.send_message(
        chat_id=message.chat_id,
        text="New response added!",
        message_thread_id=message.message_thread_id,
    )


async def handle_addglobal(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message, args: str) -> None:
    if not message.from_user or message.from_user.id != ADMIN_ID:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="You are not authorized to use this command.",
            message_thread_id=message.message_thread_id,
        )
        return
    if not message.reply_to_message:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Please reply to a message and use: /addglobal <phrase>",
            message_thread_id=message.message_thread_id,
        )
        return

    phrase = norm_key(args)
    if not phrase:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Please provide a trigger phrase.\nExample: /addglobal Hello",
            message_thread_id=message.message_thread_id,
        )
        return

    existing_row = await db.fetchone(
        "SELECT id FROM triggers WHERE is_global = ? AND search_phrase = ? LIMIT 1",
        (True, phrase),
    )
    existing_id = int(existing_row["id"]) if existing_row else 0

    try:
        mr = create_my_response_from_reply(message)
    except Exception:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Can't add this trigger",
            message_thread_id=message.message_thread_id,
        )
        return

    if not myresponse_has_content(mr):
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Can't add this trigger: replied message has no text/caption and no supported media.",
            message_thread_id=message.message_thread_id,
        )
        return

    mr.search_phrase = phrase
    ent_json = entities_to_json(mr.entities)

    if existing_id:
        await db.execute(
            """
            UPDATE triggers
            SET response = ?, file_type = ?, file_id = ?, file_name = ?, entities = ?, chat_id = ?
            WHERE id = ?
            """,
            (mr.response, str(mr.file_type), mr.file_id, mr.file_name, ent_json, 0, existing_id),
        )
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Response updated!",
            message_thread_id=message.message_thread_id,
        )
        return

    await db.execute(
        """
        INSERT INTO triggers (chat_id, search_phrase, response, file_type, file_id, file_name, entities, is_global)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        """,
        (0, phrase, mr.response, str(mr.file_type), mr.file_id, mr.file_name, ent_json, True),
    )
    await context.bot.send_message(
        chat_id=message.chat_id,
        text="New global response added!",
        message_thread_id=message.message_thread_id,
    )


# ---- Cascade triggers ----
async def handle_addc(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message, args: str) -> None:
    if not message.reply_to_message:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Please reply to the message you want to use as a response when adding a cascade trigger.\nExample: /addc Hello",
            message_thread_id=message.message_thread_id,
        )
        return

    phrase_raw = args.strip()
    if not phrase_raw:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Please provide a trigger phrase.\nExample: /addc Hello",
            message_thread_id=message.message_thread_id,
        )
        return
    phrase = norm_key(phrase_raw)

    local_exists, _ = await check_trigger_existence(db, message.chat_id, phrase)
    if local_exists:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="A local trigger with this phrase already exists. Cannot create a cascade trigger.",
            message_thread_id=message.message_thread_id,
        )
        return

    row = await db.fetchone(
        "SELECT id FROM cascade_triggers2 WHERE chat_id = ? AND search_phrase = ?",
        (message.chat_id, phrase),
    )
    if row:
        trigger_id = int(row["id"])
    else:
        _, trigger_id = await db.execute(
            "INSERT INTO cascade_triggers2 (chat_id, search_phrase) VALUES (?, ?)",
            (message.chat_id, phrase),
        )

    reply = message.reply_to_message
    response_text = (reply.text or "").strip() or (reply.caption or "").strip()
    entities = extract_reply_entities(reply)

    mr = MyResponse(search_phrase=phrase, response=response_text, entities=entities)

    if reply.photo:
        mr.file_type, mr.file_id = FILE_PHOTO, reply.photo[-1].file_id
    elif reply.animation:
        mr.file_type, mr.file_id = FILE_GIF, reply.animation.file_id
    elif reply.voice:
        mr.file_type, mr.file_id = FILE_VOICE, reply.voice.file_id
    elif reply.sticker:
        mr.file_type, mr.file_id = FILE_STICKER, reply.sticker.file_id
    elif reply.video:
        mr.file_type, mr.file_id = FILE_VIDEO, reply.video.file_id
    elif reply.document:
        mr.file_type, mr.file_id = FILE_DOCUMENT, reply.document.file_id
        mr.file_name = reply.document.file_name or ""
    elif reply.audio:
        mr.file_type, mr.file_id = FILE_AUDIO, reply.audio.file_id
        mr.file_name = reply.audio.file_name or ""
    elif reply.video_note:
        mr.file_type, mr.file_id = FILE_VIDEONOTE, reply.video_note.file_id
    elif response_text:
        pass
    else:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="The replied message has no supported content.",
            message_thread_id=message.message_thread_id,
        )
        return

    ent_json = entities_to_json(mr.entities)
    await db.execute(
        """
        INSERT INTO cascade_trigger_responses (cascade_trigger_id, response, file_type, file_id, file_name, entities)
        VALUES (?, ?, ?, ?, ?, ?)
        """,
        (trigger_id, mr.response, str(mr.file_type), mr.file_id, mr.file_name, ent_json),
    )
    await context.bot.send_message(
        chat_id=message.chat_id,
        text="Cascade trigger added successfully!",
        message_thread_id=message.message_thread_id,
    )


async def handle_removec(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message, args: str) -> None:
    # FIX: consistent strip + safer deletion conditions
    if not message.reply_to_message:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Пожалуйста, ответьте на сообщение, которое хотите удалить из каскадного триггера, и используйте /removec <фраза>.\nПример: /removec Привет",
            message_thread_id=message.message_thread_id,
        )
        return

    phrase_raw = args.strip()
    if not phrase_raw:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Пожалуйста, предоставьте фразу.\nПример: /removec Привет",
            message_thread_id=message.message_thread_id,
        )
        return
    phrase = norm_key(phrase_raw)

    r = message.reply_to_message
    response_text = ((r.text or "") or (r.caption or "")).strip()

    file_id = ""
    if r.photo:
        file_id = r.photo[-1].file_id
    elif r.animation:
        file_id = r.animation.file_id
    elif r.voice:
        file_id = r.voice.file_id
    elif r.sticker:
        file_id = r.sticker.file_id
    elif r.video:
        file_id = r.video.file_id
    elif r.document:
        file_id = r.document.file_id
    elif r.audio:
        file_id = r.audio.file_id
    elif r.video_note:
        file_id = r.video_note.file_id

    if not response_text and not file_id:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Сообщение не содержит текста/подписи/медиа.",
            message_thread_id=message.message_thread_id,
        )
        return

    row = await db.fetchone(
        "SELECT id FROM cascade_triggers2 WHERE chat_id = ? AND search_phrase = ?",
        (message.chat_id, phrase),
    )
    if not row:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Каскадный триггер с данной фразой не найден.",
            message_thread_id=message.message_thread_id,
        )
        return
    trigger_id = int(row["id"])

    if file_id:
        deleted, _ = await db.execute(
            "DELETE FROM cascade_trigger_responses WHERE cascade_trigger_id = ? AND file_id = ?",
            (trigger_id, file_id),
        )
        if deleted == 0:
            await context.bot.send_message(
                chat_id=message.chat_id,
                text="Медиа-ответ с данным FileID не найден.",
                message_thread_id=message.message_thread_id,
            )
            return
        await context.bot.send_message(
            chat_id=message.chat_id,
            text=f"Медиа-ответ удалён из каскадного триггера '{phrase}'.",
            message_thread_id=message.message_thread_id,
        )
    else:
        deleted, _ = await db.execute(
            """
            DELETE FROM cascade_trigger_responses
            WHERE cascade_trigger_id = ?
              AND COALESCE(file_id, '') = ''
              AND response = ?
            """,
            (trigger_id, response_text),
        )
        if deleted == 0:
            await context.bot.send_message(
                chat_id=message.chat_id,
                text="Текстовый ответ не найден.",
                message_thread_id=message.message_thread_id,
            )
            return
        await context.bot.send_message(
            chat_id=message.chat_id,
            text=f"Текстовый ответ удалён из каскадного триггера '{phrase}'.",
            message_thread_id=message.message_thread_id,
        )

    row2 = await db.fetchone(
        "SELECT COUNT(*) AS c FROM cascade_trigger_responses WHERE cascade_trigger_id = ?",
        (trigger_id,),
    )
    remaining = int(row2["c"]) if row2 else 0
    if remaining == 0:
        await db.execute("DELETE FROM cascade_triggers2 WHERE id = ?", (trigger_id,))
        await context.bot.send_message(
            chat_id=message.chat_id,
            text=f"Каскадный триггер '{phrase}' удалён, так как больше не содержит ответов.",
            message_thread_id=message.message_thread_id,
        )


async def handle_cascade_triggers(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message) -> None:
    content = norm_key(message.text) if message.text else norm_key(message.caption) if message.caption else ""

    rows = await db.fetchall(
        """
        SELECT
            ct.id AS trigger_id,
            ct.search_phrase,
            COALESCE(ctr.id, ctr.rowid) AS resp_id,
            ctr.response,
            ctr.file_type,
            ctr.file_id,
            ctr.file_name,
            ctr.entities
        FROM cascade_triggers2 ct
        JOIN cascade_trigger_responses ctr ON ct.id = ctr.cascade_trigger_id
        WHERE ct.chat_id = ? AND ct.search_phrase = ?
        ORDER BY ctr.id ASC
        """,
        (message.chat_id, content),
    )
    if not rows:
        return

    for r in rows:
        resp = MyResponse(
            id=int(r["resp_id"]),
            search_phrase=r["search_phrase"] or "",
            response=r["response"] or "",
            file_type=FileType(r["file_type"] or ""),
            file_id=r["file_id"] or "",
            file_name=r["file_name"] or "",
            entities=entities_from_json(r["entities"] or ""),
        )
        await send_trigger_response(context, message, resp, reply_to=False)


# -----------------------------
# Timezone commands / updater
# -----------------------------
async def time_add(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message, args: str) -> None:
    location = args.strip()
    if not location:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Usage: /addlocation <Location>",
            message_thread_id=message.message_thread_id,
        )
        return

    try:
        _ = get_current_time_for_location(location)
    except Exception:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="This location is not available. Please try a different town in this time zone.",
            message_thread_id=message.message_thread_id,
        )
        return

    try:
        await db.execute("INSERT INTO timezones (chatID, location) VALUES (?, ?)", (message.chat_id, location))
    except aiosqlite.Error as e:
        if "UNIQUE constraint failed" in str(e):
            await context.bot.send_message(
                chat_id=message.chat_id,
                text="This location has already been added.",
                message_thread_id=message.message_thread_id,
            )
            return
        raise

    rows = await db.fetchall("SELECT location FROM timezones WHERE chatID = ?", (message.chat_id,))
    locations = [r["location"] for r in rows]

    await context.bot.send_message(
        chat_id=message.chat_id,
        text=f"Timezone location '{location}' added successfully!",
        message_thread_id=message.message_thread_id,
    )
    await context.bot.send_message(
        chat_id=message.chat_id,
        text="Current locations are: " + ", ".join(locations),
        message_thread_id=message.message_thread_id,
    )


async def time_remove(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message, args: str) -> None:
    location = args.strip()
    if not location:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Usage: /removelocation <Location>",
            message_thread_id=message.message_thread_id,
        )
        return
    deleted, _ = await db.execute("DELETE FROM timezones WHERE chatID = ? AND location = ?", (message.chat_id, location))
    txt = (
        f"Timezone location '{location}' removed successfully!"
        if deleted > 0
        else f"No timezone location found for '{location}'."
    )
    await context.bot.send_message(chat_id=message.chat_id, text=txt, message_thread_id=message.message_thread_id)


async def add_or_update_alias(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message, args: str) -> None:
    parts = args.split("-", 1)
    if len(parts) != 2:
        await context.bot.send_message(
            chat_id=message.chat_id,
            text="Invalid format. Please use 'Location - Alias'.",
            message_thread_id=message.message_thread_id,
        )
        return
    location = parts[0].strip()
    alias = parts[1].strip()

    row = await db.fetchone(
        "SELECT 1 AS x FROM alias WHERE chatID = ? AND location = ? LIMIT 1",
        (message.chat_id, location),
    )
    if row:
        await db.execute("UPDATE alias SET alias = ? WHERE chatID = ? AND location = ?", (alias, message.chat_id, location))
    else:
        await db.execute("INSERT INTO alias (chatID, location, alias) VALUES (?, ?, ?)", (message.chat_id, location, alias))

    await context.bot.send_message(
        chat_id=message.chat_id,
        text=f"Alias '{alias}' set for location '{location}'",
        message_thread_id=message.message_thread_id,
    )


async def reset_message(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message) -> None:
    await db.execute("DELETE FROM messagelist WHERE chatID = ?", (message.chat_id,))
    await update_time_message_for_chat(db, context, message.chat_id)


async def update_time_message_for_chat(db: Database, context: ContextTypes.DEFAULT_TYPE, chat_id: int) -> None:
    rows = await db.fetchall(
        """
        SELECT tz.location, COALESCE(a.alias, tz.location) AS display_location
        FROM timezones tz
        LEFT JOIN alias a ON tz.chatID = a.chatID AND tz.location = a.location
        WHERE tz.chatID = ?
        """,
        (chat_id,),
    )

    locs: List[Tuple[str, str, datetime]] = []
    for r in rows:
        loc = r["location"]
        display = r["display_location"]
        try:
            now = get_current_time_for_location(loc)
            locs.append((loc, display, now))
        except Exception:
            continue

    if not locs:
        return

    locs.sort(key=lambda x: (x[2].date().toordinal(), x[2].hour, x[2].minute))
    text = "\n".join([f"{display} {dt.strftime('%H:%M')}" for _, display, dt in locs])

    row = await db.fetchone("SELECT messageID FROM messagelist WHERE chatID = ?", (chat_id,))
    bot = context.bot

    if row is None:
        sent = await bot.send_message(chat_id=chat_id, text=text)
        await db.execute("INSERT INTO messagelist (chatID, messageID) VALUES (?, ?)", (chat_id, sent.message_id))
        return

    msg_id = int(row["messageID"])
    try:
        await bot.edit_message_text(chat_id=chat_id, message_id=msg_id, text=text)
    except BadRequest as e:
        emsg = str(e)
        if "message to edit not found" in emsg or "message can't be edited" in emsg:
            await db.execute("DELETE FROM messagelist WHERE chatID = ?", (chat_id,))
            sent = await bot.send_message(chat_id=chat_id, text=text)
            await db.execute("INSERT INTO messagelist (chatID, messageID) VALUES (?, ?)", (chat_id, sent.message_id))
    except Forbidden:
        return


async def update_time_job(context: ContextTypes.DEFAULT_TYPE) -> None:
    db: Database = context.application.bot_data["db"]
    chat_ids = await db.get_all_active_chat_ids()
    for cid in chat_ids:
        await update_time_message_for_chat(db, context, cid)


# -----------------------------
# Special behaviors
# -----------------------------
async def handle_new_member(context: ContextTypes.DEFAULT_TYPE, message: Message) -> None:
    if not message.new_chat_members:
        return
    sticker_set_name = "privetcivpack_by_fStikBot"
    try:
        stset = await context.bot.get_sticker_set(name=sticker_set_name)
        if not stset.stickers:
            return
        st = random.choice(stset.stickers)
        await context.bot.send_sticker(
            chat_id=message.chat_id,
            sticker=st.file_id,
            message_thread_id=message.message_thread_id,
        )
    except Exception as e:
        logger.info("handle_new_member failed: %s", e)


async def maybe_send_mention_memes(context: ContextTypes.DEFAULT_TYPE, message: Message) -> bool:
    """
    Returns True only if we actually sent something (so we should stop further processing).
    """
    text = message.text or ""
    if not text:
        return False

    try:
        current_moscow = get_current_time_for_location("Moscow")
    except Exception:
        current_moscow = datetime.now(timezone.utc)

    if message.chat_id in (-1001245934322, -1001390115843) and "@Porky8888" in text:
        la = datetime.now(ZoneInfo("America/Los_Angeles"))
        if not is_time_between(la, 2, 7):
            return False
        if random.random() < 0.5:
            return False
        file_id = "AgACAgQAAx0Cc2pGjQACAUBlssL7rSKP4mmzMMYeORKjAS3LOAACHMIxGzznmFF5Spk5RRTfbwEAAwIAA3gAAzQE"
        caption = f"Машталер в {la.hour} ночи" if is_time_between(la, 2, 4) else f"Машталер в {la.hour} утра"
        await context.bot.send_photo(
            chat_id=message.chat_id,
            photo=file_id,
            caption=caption,
            reply_to_message_id=message.message_id,
            message_thread_id=message.message_thread_id,
        )
        return True

    if message.chat_id == -1001970411651 and "@vincenitycarter" in text and is_time_between_19_and_8(current_moscow):
        file_id = "AgACAgQAAx0Cc2pGjQACAX9ltZ3416cTOKI_-1Jp1wXzAVCLygACG74xGwkasVEOQZYuKQ4abQEAAwIAA3kAAzUE"
        if random.random() < 0.5:
            file_id = "AgACAgIAAx0Cc2pGjQACAnVmeHhbXkkqgeg_DNEW1dChwB3BYQACuNoxG2g9yUsZaxbgiGFD_wEAAwIAA3kAAzUE"
        caption = f"Сегодня, в {current_moscow.strftime('%H:%M')}, Яков Андреев был найден спящим в своей квартире. Приносим соболезнования всем его тиммейтам"
        await context.bot.send_photo(
            chat_id=message.chat_id,
            photo=file_id,
            caption=caption,
            reply_to_message_id=message.message_id,
            message_thread_id=message.message_thread_id,
        )
        return True

    if message.chat_id in (-1002245157577, -1001936344717) and "@KelThuzad" in text:
        ny = datetime.now(ZoneInfo("America/New_York"))
        if not is_time_between(ny, 2, 7):
            return False
        if message.chat_id == -1002245157577 and random.random() < 0.3:
            return False
        file_id = "AgACAgQAAx0Cc2pGjQACArNm0PVZDzYsYwqBhiOBkCD4rCu8cQAC-78xGxt-iFJZyKNkTiV9hQEAAwIAA3gAAzUE"
        caption = f"Кел в {ny.hour} ночи" if is_time_between(ny, 2, 4) else f"Кел в {ny.hour} утра"
        await context.bot.send_photo(
            chat_id=message.chat_id,
            photo=file_id,
            caption=caption,
            reply_to_message_id=message.message_id,
            message_thread_id=message.message_thread_id,
        )
        return True

    return False


# -----------------------------
# Main message handler
# -----------------------------
async def handle_message(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message or not message.from_user:
        return

    db: Database = context.application.bot_data["db"]

    target_chat_id = -1002245157577
    keyword = "рейдодроч"
    txt_for_keyword = message.text or message.caption or ""
    if message.chat_id == target_chat_id and norm_key(txt_for_keyword) == norm_key(keyword):
        ok = await check_and_update_last_keyword(target_chat_id, keyword)
        if not ok:
            logger.info("Keyword cooldown: skipping.")
            return

    if message.new_chat_members:
        await handle_new_member(context, message)

    await handle_terpet_message(db, context, message)

    cmd, args = parse_command_and_args(message, getattr(context.bot, "username", None))

    if cmd == "topterpil":
        await handle_topterpil(db, context, message)
        return

    if cmd == "getlink":
        await handle_getlink(context, message, args)
        return

    if message.chat.type not in (ChatType.GROUP, ChatType.SUPERGROUP):
        if message.chat.type == ChatType.PRIVATE:
            if message.forward_from:
                txt = f"<b>Message forwarded from User ID: </b> <code>{message.forward_from.id}</code>"
                await context.bot.send_message(
                    chat_id=message.chat_id,
                    text=txt,
                    parse_mode=ParseMode.HTML,
                    message_thread_id=message.message_thread_id,
                )
                return
            if message.forward_sender_name:
                await context.bot.send_message(
                    chat_id=message.chat_id,
                    text="<b>Sorry, this user's ID is hidden</b>",
                    parse_mode=ParseMode.HTML,
                    message_thread_id=message.message_thread_id,
                )
                return
            txt = f"<b>Your User ID:</b> <code>{message.from_user.id}</code>"
            await context.bot.send_message(
                chat_id=message.chat_id,
                text=txt,
                parse_mode=ParseMode.HTML,
                message_thread_id=message.message_thread_id,
            )
            return
        return

    if cmd in {"add", "remove", "addglobal", "removeglobal", "triggers"}:
        allowed_without_reply = {"removeglobal", "triggers", "remove"}
        if cmd in allowed_without_reply or message.reply_to_message:
            if cmd == "add":
                await handle_add(db, context, message, args)
                return
            if cmd == "remove":
                await handle_remove(db, context, message, args)
                return
            if cmd == "addglobal":
                await handle_addglobal(db, context, message, args)
                return
            if cmd == "removeglobal":
                await handle_removeglobal(db, context, message, args)
                return
            if cmd == "triggers":
                await handle_triggers_command(db, context, message)
                return

    if cmd == "chatid":
        await handle_chatid(context, message)
        return
    if cmd == "generateqr":
        await handle_generate_qr(context, message, args)
        return
    if cmd == "generatebar":
        await handle_generate_barcode(context, message, args)
        return
    if cmd == "addlocation":
        await time_add(db, context, message, args)
        return
    if cmd == "removelocation":
        await time_remove(db, context, message, args)
        return
    if cmd == "alias":
        await add_or_update_alias(db, context, message, args)
        return
    if cmd == "resetmessage":
        await reset_message(db, context, message)
        return
    if cmd == "samplesize":
        await handle_samplesize(context, message)
        return

    roll_map = {
        "roll": 100,
        "roll20": 20,
        "roll12": 12,
        "roll10": 10,
        "roll8": 8,
        "roll6": 6,
        "roll4": 4,
    }
    if cmd in roll_map:
        await handle_roll(context, message, roll_map[cmd])
        return

    if cmd == "addc":
        await handle_addc(db, context, message, args)
        return
    if cmd == "removec":
        await handle_removec(db, context, message, args)
        return

    if await maybe_send_mention_memes(context, message):
        return

    # ---- Fast trigger lookup (no full-table scans) ----
    # FIX #3: match normal triggers on text OR caption
    received_key = norm_key(message.text or message.caption or "")
    if received_key:
        row = await db.fetchone(
            """
            SELECT id, search_phrase, response, file_type, file_id, file_name, entities
            FROM triggers
            WHERE chat_id = ? AND is_global = ? AND search_phrase = ?
            LIMIT 1
            """,
            (message.chat_id, False, received_key),
        )
        if row:
            resp = MyResponse(
                id=int(row["id"]),
                search_phrase=row["search_phrase"] or "",
                response=row["response"] or "",
                file_type=FileType(row["file_type"] or ""),
                file_id=row["file_id"] or "",
                file_name=row["file_name"] or "",
                entities=entities_from_json(row["entities"] or ""),
            )
            await send_trigger_response(context, message, resp, reply_to=True)
        else:
            rowg = await db.fetchone(
                """
                SELECT id, search_phrase, response, file_type, file_id, file_name, entities
                FROM triggers
                WHERE is_global = ? AND search_phrase = ?
                LIMIT 1
                """,
                (True, received_key),
            )
            if rowg:
                resp = MyResponse(
                    id=int(rowg["id"]),
                    search_phrase=rowg["search_phrase"] or "",
                    response=rowg["response"] or "",
                    file_type=FileType(rowg["file_type"] or ""),
                    file_id=rowg["file_id"] or "",
                    file_name=rowg["file_name"] or "",
                    entities=entities_from_json(rowg["entities"] or ""),
                )
                await send_trigger_response(context, message, resp, reply_to=True)

    await handle_cascade_triggers(db, context, message)


# -----------------------------
# Startup / Shutdown
# -----------------------------
async def on_startup(app: Application) -> None:
    db = Database(DB_PATH)
    await db.connect()
    await db.init_schema()

    app.bot_data["db"] = db

    chat_ids = await db.get_all_active_chat_ids()

    class _TmpCtx:
        def __init__(self, application: Application) -> None:
            self.application = application
            self.bot = application.bot

    tmp_ctx = _TmpCtx(app)  # type: ignore
    for cid in chat_ids:
        await update_time_message_for_chat(db, tmp_ctx, cid)  # type: ignore

    # FIX #6: JobQueue optional dependency
    if app.job_queue:
        app.job_queue.run_repeating(update_time_job, interval=30, first=30)
    else:
        logger.warning("JobQueue is not available (install python-telegram-bot[job-queue]); timezone updates disabled.")

    logger.info("Bot started.")


async def on_shutdown(app: Application) -> None:
    db: Database = app.bot_data.get("db")
    if db:
        await db.close()
    logger.info("Bot stopped.")


# -----------------------------
# Entrypoint
# -----------------------------
def main() -> None:
    token = read_bot_token(BOT_TOKEN_PATH)

    app = (
        Application.builder()
        .token(token)
        .post_init(on_startup)
        .post_shutdown(on_shutdown)
        .build()
    )

    app.add_handler(MessageHandler(filters.ALL, handle_message))
    app.run_polling(allowed_updates=Update.ALL_TYPES)


if __name__ == "__main__":
    main()
