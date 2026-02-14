#!/usr/bin/env python3.12
from __future__ import annotations

import asyncio
import json
import logging
import math
import os
import random
import re
import time
import sys
import unicodedata
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from functools import partial
from io import BytesIO
from typing import Any, Dict, List, Optional, Sequence, Tuple
from zoneinfo import ZoneInfo

import aiosqlite
import jwt
import qrcode
from barcode import Code128
from barcode.writer import ImageWriter

from telegram import Message, MessageEntity, Update
from telegram.constants import ParseMode
from telegram.error import BadRequest, Forbidden
from telegram.ext import (
    Application,
    CommandHandler,
    ContextTypes,
    MessageHandler,
    filters,
)

# -----------------------------
# Logging Configuration
# -----------------------------
logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(name)s: %(message)s",
)
logger = logging.getLogger("tg-bot")

# -----------------------------
# Log Security: Redaction Filter
# -----------------------------
class TokenRedactionFilter(logging.Filter):
    """Redacts bot token from logs."""

    def __init__(self, token: str):
        super().__init__()
        self.token = token

    def filter(self, record: logging.LogRecord) -> bool:
        if not self.token:
            return True

        if isinstance(record.msg, str) and self.token in record.msg:
            record.msg = record.msg.replace(self.token, "********")

        if record.args:
            new_args = []
            for arg in record.args:
                if isinstance(arg, str):
                    arg = arg.replace(self.token, "********")
                new_args.append(arg)
            record.args = tuple(new_args)
        return True


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
APP_SECRET_PATH = "./config/appsecret.txt"
ALLOWED_USERS_PATH = "./config/allowed_users.json"
DB_PATH = "./mydb.db"

JITSI_APP_ID = "longspear"
JITSI_DOMAIN = "call.ivansavelyev.ru"
JITSI_BASE_URL = "https://call.ivansavelyev.ru"
JITSI_TOKEN_TTL_SECONDS = 60 * 60 * 5

ADMIN_ID = 193117018
BLOCKED_COMMAND_USER_ID = 89886125

FILENAME_SANITIZER = re.compile(r"[^a-zA-Z0-9.\-]")

# -----------------------------
# Keyword cooldown (5 minutes)
# -----------------------------
_last_keyword_timestamps: Dict[str, datetime] = {}
_last_keyword_lock = asyncio.Lock()
_allowed_users_lock = asyncio.Lock()


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
    env_token = os.getenv("BOT_TOKEN") or os.getenv("TELEGRAM_BOT_TOKEN")
    if env_token:
        return env_token.strip()
    with open(path, "r", encoding="utf-8") as f:
        return f.readline().strip()


def read_app_secret(path: str) -> str:
    env_secret = os.getenv("JITSI_APP_SECRET")
    if env_secret:
        return env_secret.strip()
    with open(path, "r", encoding="utf-8") as f:
        return f.read().strip()


def nfc_casefold(s: str) -> str:
    return unicodedata.normalize("NFC", s).casefold()


def norm_key(s: Optional[str]) -> str:
    return nfc_casefold((s or "").strip())


def sanitize_filename(name: str) -> str:
    return FILENAME_SANITIZER.sub("_", name)


def safe_int(v: Any, default: int = 0) -> int:
    try:
        if v is None:
            return default
        return int(v)
    except (TypeError, ValueError):
        return default


def _ensure_parent_dir(path: str) -> None:
    parent = os.path.dirname(path)
    if parent:
        os.makedirs(parent, exist_ok=True)


def _load_allowed_users_sync(path: str) -> Dict[str, Any]:
    if not os.path.exists(path):
        return {"allowed_users": {}}
    try:
        with open(path, "r", encoding="utf-8") as f:
            data = json.load(f)
    except (json.JSONDecodeError, OSError) as exc:
        logger.warning("Failed to read allowed users file (%s): %s", path, exc)
        return {"allowed_users": {}}
    if not isinstance(data, dict):
        return {"allowed_users": {}}
    allowed = data.get("allowed_users")
    if not isinstance(allowed, dict):
        data["allowed_users"] = {}
    return data


def _save_allowed_users_sync(path: str, data: Dict[str, Any]) -> None:
    _ensure_parent_dir(path)
    tmp_path = f"{path}.tmp"
    with open(tmp_path, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2, sort_keys=True)
    os.replace(tmp_path, path)


async def load_allowed_users(path: str) -> Dict[str, Any]:
    return await asyncio.to_thread(_load_allowed_users_sync, path)


async def save_allowed_users(path: str, data: Dict[str, Any]) -> None:
    await asyncio.to_thread(_save_allowed_users_sync, path, data)


def _get_allowed_users(data: Dict[str, Any]) -> Dict[str, Dict[str, str]]:
    allowed = data.get("allowed_users")
    if not isinstance(allowed, dict):
        return {}
    return allowed


def _is_admin(user_id: int) -> bool:
    return user_id == ADMIN_ID


def _is_allowed_user(user_id: int, data: Dict[str, Any]) -> bool:
    return str(user_id) in _get_allowed_users(data)


def generate_jitsi_link(user_name: str, user_id: str, app_secret: str) -> str:
    room_name = uuid.uuid4().hex[:12]
    now = int(time.time())
    payload = {
        "aud": "jitsi",
        "iss": JITSI_APP_ID,
        "sub": JITSI_DOMAIN,
        "room": room_name,
        "exp": now + JITSI_TOKEN_TTL_SECONDS,
        "nbf": now,
        "iat": now,
        "context": {
            "user": {
                "name": user_name,
                "id": str(user_id),
            }
        },
    }
    token = jwt.encode(payload, app_secret, algorithm="HS256")
    return f"{JITSI_BASE_URL}/{room_name}?jwt={token}"


def _is_topic_error(e: Exception) -> bool:
    s = str(e).lower()
    return ("topic_closed" in s) or ("topic closed" in s)


def _is_thread_not_found(e: Exception) -> bool:
    s = str(e).lower()
    return "message thread not found" in s


async def _call_with_thread_fallback(fn, **kwargs):
    """
    Try request; if Telegram says message_thread_id is invalid -> retry without it.
    If topic is closed -> silently skip.
    """
    try:
        return await fn(**kwargs)
    except BadRequest as e:
        if _is_topic_error(e):
            logger.info("Skip send: topic closed (%s)", e)
            return None

        if _is_thread_not_found(e):
            # Retry without thread id first
            kwargs2 = dict(kwargs)
            kwargs2.pop("message_thread_id", None)
            try:
                return await fn(**kwargs2)
            except BadRequest as e2:
                if _is_topic_error(e2):
                    logger.info("Skip send: topic closed (retry) (%s)", e2)
                    return None
                # If still failing, drop reply_to (can also be thread-related)
                if _is_thread_not_found(e2):
                    kwargs3 = dict(kwargs2)
                    kwargs3.pop("reply_to_message_id", None)
                    return await fn(**kwargs3)
                raise

        raise


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
        await self.conn.execute("PRAGMA journal_mode=WAL;")
        await self.conn.execute("PRAGMA foreign_keys = ON;")
        await self.conn.commit()

    async def close(self) -> None:
        if self.conn is not None:
            await self.conn.close()
            self.conn = None

    async def execute(self, sql: str, params: Sequence[Any] = ()) -> Tuple[int, int]:
        assert self.conn is not None
        async with self.lock:
            async with self.conn.execute(sql, params) as cur:
                await self.conn.commit()
                return cur.rowcount, cur.lastrowid

    async def fetchone(self, sql: str, params: Sequence[Any] = ()) -> Optional[aiosqlite.Row]:
        assert self.conn is not None
        async with self.conn.execute(sql, params) as cur:
            return await cur.fetchone()

    async def fetchall(self, sql: str, params: Sequence[Any] = ()) -> List[aiosqlite.Row]:
        assert self.conn is not None
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

        await self.execute("CREATE INDEX IF NOT EXISTS idx_triggers_local ON triggers(chat_id, is_global, search_phrase)")
        await self.execute("CREATE INDEX IF NOT EXISTS idx_triggers_global ON triggers(is_global, search_phrase)")
        await self.execute("CREATE INDEX IF NOT EXISTS idx_cascade_phrase ON cascade_triggers2(chat_id, search_phrase)")
        await self.execute("CREATE INDEX IF NOT EXISTS idx_cascade_resp ON cascade_trigger_responses(cascade_trigger_id)")

        try:
            await self.execute(
                "CREATE UNIQUE INDEX IF NOT EXISTS idx_triggers_unique ON triggers(chat_id, is_global, search_phrase)"
            )
        except Exception as e:
            logger.warning("Failed to create triggers UNIQUE index: %s", e)

    async def get_all_active_chat_ids(self) -> List[int]:
        rows = await self.fetchall("SELECT DISTINCT chatID FROM timezones")
        return [safe_int(r["chatID"]) for r in rows]


# -----------------------------
# Timezone helpers
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
        return [MessageEntity(**d) for d in data]
    except Exception:
        return []


# -----------------------------
# Sending responses
# -----------------------------
def _base_send_kwargs(message: Message, reply_to_message_id: Optional[int]) -> Dict[str, Any]:
    """
    Important fix:
    - DO NOT pass message_thread_id when it is None (Telegram can error: "Message thread not found").
    - DO NOT pass reply_to_message_id when it is None.
    """
    kwargs: Dict[str, Any] = {"chat_id": message.chat_id}

    if reply_to_message_id is not None:
        kwargs["reply_to_message_id"] = reply_to_message_id

    if message.message_thread_id is not None:
        kwargs["message_thread_id"] = message.message_thread_id

    return kwargs


async def send_trigger_response(
    context: ContextTypes.DEFAULT_TYPE,
    message: Message,
    resp: MyResponse,
    reply_to: bool,
) -> None:
    bot = context.bot
    reply_to_message_id = message.message_id if reply_to else None
    text = resp.response or ""
    ft = resp.file_type or FileType("")

    kwargs = _base_send_kwargs(message, reply_to_message_id)

    if ft == FILE_PHOTO:
        await _call_with_thread_fallback(
            bot.send_photo,
            photo=resp.file_id,
            caption=text or None,
            caption_entities=resp.entities or None,
            **kwargs,
        )
    elif ft == FILE_GIF:
        await _call_with_thread_fallback(
            bot.send_animation,
            animation=resp.file_id,
            caption=text or None,
            caption_entities=resp.entities or None,
            **kwargs,
        )
    elif ft == FILE_VOICE:
        await _call_with_thread_fallback(bot.send_voice, voice=resp.file_id, **kwargs)
    elif ft == FILE_STICKER:
        await _call_with_thread_fallback(bot.send_sticker, sticker=resp.file_id, **kwargs)
    elif ft == FILE_VIDEO:
        await _call_with_thread_fallback(
            bot.send_video,
            video=resp.file_id,
            caption=text or None,
            caption_entities=resp.entities or None,
            **kwargs,
        )
    elif ft == FILE_DOCUMENT:
        await _call_with_thread_fallback(
            bot.send_document,
            document=resp.file_id,
            caption=text or None,
            caption_entities=resp.entities or None,
            **kwargs,
        )
    elif ft == FILE_VIDEONOTE:
        await _call_with_thread_fallback(bot.send_video_note, video_note=resp.file_id, **kwargs)
    elif ft == FILE_AUDIO:
        await _call_with_thread_fallback(
            bot.send_audio,
            audio=resp.file_id,
            caption=text or None,
            caption_entities=resp.entities or None,
            **kwargs,
        )
    else:
        if text.strip():
            await _call_with_thread_fallback(bot.send_message, text=text, entities=resp.entities or None, **kwargs)


async def send_long_text(
    context: ContextTypes.DEFAULT_TYPE,
    chat_id: int,
    text: str,
    message_thread_id: Optional[int] = None,
) -> None:
    limit = 3800
    s = text or ""

    base_kwargs: Dict[str, Any] = {"chat_id": chat_id}
    if message_thread_id is not None:
        base_kwargs["message_thread_id"] = message_thread_id

    while s:
        if len(s) <= limit:
            await _call_with_thread_fallback(context.bot.send_message, text=s, **base_kwargs)
            return
        cut = s.rfind("\n", 0, limit)
        if cut <= 0:
            cut = limit
        chunk = s[:cut].rstrip()
        s = s[cut:].lstrip("\n")
        if chunk:
            await _call_with_thread_fallback(context.bot.send_message, text=chunk, **base_kwargs)


# -----------------------------
# Handlers
# -----------------------------
async def handle_chatid(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    kwargs: Dict[str, Any] = {"chat_id": message.chat_id}
    if message.message_thread_id is not None:
        kwargs["message_thread_id"] = message.message_thread_id
    await _call_with_thread_fallback(context.bot.send_message, text=f"This chat ID is: {message.chat_id}", **kwargs)


async def handle_getlink(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message or not message.from_user or message.from_user.id != ADMIN_ID:
        return

    if not context.args:
        await message.reply_text("Please provide an ID.")
        return

    user_id = context.args[0].strip()
    link = f"<a href='tg://user?id={user_id}'>Link to User</a>"

    kwargs: Dict[str, Any] = {"chat_id": message.chat_id}
    if message.message_thread_id is not None:
        kwargs["message_thread_id"] = message.message_thread_id

    await _call_with_thread_fallback(
        context.bot.send_message,
        text=link,
        parse_mode=ParseMode.HTML,
        disable_web_page_preview=True,
        **kwargs,
    )


async def handle_roll(update: Update, context: ContextTypes.DEFAULT_TYPE, sides: int = 100) -> None:
    message = update.effective_message
    if not message:
        return
    n = random.randint(1, sides)

    kwargs = _base_send_kwargs(message, reply_to_message_id=message.message_id)
    await _call_with_thread_fallback(context.bot.send_message, text=f"🎲 You rolled: {n}", **kwargs)


# -----------------------------
# Blocking I/O Helpers
# -----------------------------
def _generate_qr_sync(code: str) -> BytesIO:
    img = qrcode.make(code)
    bio = BytesIO()
    bio.name = f"{sanitize_filename(code)[:40]}.png"
    img.save(bio, format="PNG")
    bio.seek(0)
    return bio


def _generate_bar_sync(code: str) -> BytesIO:
    barcode_obj = Code128(code, writer=ImageWriter())
    bio = BytesIO()
    bio.name = f"{sanitize_filename(code)[:40]}.png"
    barcode_obj.write(bio, options={"write_text": False})
    bio.seek(0)
    return bio


async def handle_generate_qr(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    args = " ".join(context.args) if context.args else ""
    if not args:
        await message.reply_text("Usage: /generateqr <text>")
        return
    loop = asyncio.get_running_loop()
    bio = await loop.run_in_executor(None, _generate_qr_sync, args)
    await message.reply_photo(photo=bio)


async def handle_generate_barcode(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    args = " ".join(context.args) if context.args else ""
    if not args:
        await message.reply_text("Usage: /generatebar <text>")
        return
    loop = asyncio.get_running_loop()
    bio = await loop.run_in_executor(None, _generate_bar_sync, args)
    await message.reply_photo(photo=bio)


# -----------------------------
# Sample Size Logic
# -----------------------------
@dataclass
class SampleSize:
    low: int
    medium: int
    high: int


def get_risk_size(sizes: SampleSize, risk: str) -> int:
    return getattr(sizes, risk, 0)


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

    for i, (_, maxp, sizes) in enumerate(rows):
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


async def handle_samplesize(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    if not context.args or len(context.args) < 2:
        await message.reply_text("Usage: /samplesize <low|medium|high> <population>")
        return

    risk = context.args[0].casefold()
    try:
        population = int(context.args[1])
    except ValueError:
        await message.reply_text(f"Invalid population number: {context.args[1]}")
        return

    try:
        ss = get_sample_size(population, risk)
        if ss <= 0:
            raise ValueError("Risk must be one of: low, medium, high")
    except Exception as e:
        await message.reply_text(str(e))
        return

    selection = generate_random_selection(ss, population)
    out = f"For {risk} risk and population of {population}, sample size is {ss}\n"
    out += "Random numbers for random selection:\n" + "\n".join(map(str, selection))
    await message.reply_text(out)


# -----------------------------
# Jitsi Meet Rooms
# -----------------------------
async def handle_allow_create(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message or not message.from_user:
        return
    if not _is_admin(message.from_user.id):
        await message.reply_text("You are not authorized.")
        return
    if not message.reply_to_message or not message.reply_to_message.from_user:
        await message.reply_text("Ответь на сообщение пользователя")
        return
    target = message.reply_to_message.from_user
    added_at = datetime.now(timezone.utc).date().isoformat()
    async with _allowed_users_lock:
        data = context.application.bot_data.setdefault("allowed_users", {"allowed_users": {}})
        allowed = data.setdefault("allowed_users", {})
        allowed[str(target.id)] = {"name": target.full_name or "", "added_at": added_at}
        await save_allowed_users(ALLOWED_USERS_PATH, data)
    await message.reply_text(f"✅ {target.full_name} теперь может создавать комнаты")


async def handle_ban_create(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message or not message.from_user:
        return
    if not _is_admin(message.from_user.id):
        await message.reply_text("You are not authorized.")
        return
    if not message.reply_to_message or not message.reply_to_message.from_user:
        await message.reply_text("Ответь на сообщение пользователя")
        return
    target = message.reply_to_message.from_user
    async with _allowed_users_lock:
        data = context.application.bot_data.setdefault("allowed_users", {"allowed_users": {}})
        allowed = data.setdefault("allowed_users", {})
        if str(target.id) in allowed:
            allowed.pop(str(target.id))
            await save_allowed_users(ALLOWED_USERS_PATH, data)
    await message.reply_text(f"🚫 {target.full_name} больше не может создавать комнаты")


async def handle_create_call(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message or not message.from_user:
        return
    user_id = message.from_user.id
    async with _allowed_users_lock:
        data = context.application.bot_data.get("allowed_users", {"allowed_users": {}})
        allowed = _is_allowed_user(user_id, data)
    if not _is_admin(user_id) and not allowed:
        await message.reply_text("У тебя нет доступа к созданию комнат")
        return
    app_secret = context.application.bot_data.get("jitsi_app_secret", "")
    if not app_secret:
        await message.reply_text("Секрет не настроен. Добавь ./config/appsecret.txt")
        return
    link = generate_jitsi_link(
        user_name=message.from_user.full_name or "User",
        user_id=str(user_id),
        app_secret=app_secret,
    )
    await message.reply_text(
        f"<a href=\"{link}\">вот ваша ссылка</a>",
        parse_mode=ParseMode.HTML,
        disable_web_page_preview=True,
    )


# -----------------------------
# Terpet & Topterpil
# -----------------------------
def get_raz_form(count: int) -> str:
    if count % 10 == 1 and count % 100 != 11:
        return "раз"
    if 2 <= (count % 10) <= 4 and not (12 <= (count % 100) <= 14):
        return "раза"
    return "раз"


async def increment_terpet(db: Database, context: ContextTypes.DEFAULT_TYPE, message: Message) -> None:
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
    count = safe_int(row["count"], 1) if row else 1

    kwargs = _base_send_kwargs(message, reply_to_message_id=message.message_id)
    await _call_with_thread_fallback(
        context.bot.send_message,
        text=f"Вы терпели {count} {get_raz_form(count)}",
        **kwargs,
    )


async def handle_terpet_command(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    if message.chat_id != -1001390115843:
        return
    db: Database = context.application.bot_data["db"]
    await increment_terpet(db, context, message)


async def handle_topterpil(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    db: Database = context.application.bot_data["db"]
    rows = await db.fetchall(
        """
        SELECT COALESCE(NULLIF(username, ''), first_name) AS name, count
        FROM terpet_count
        ORDER BY count DESC
        LIMIT 5
        """
    )
    if not rows:
        await update.effective_message.reply_text("No terpet data available.")
        return
    lines = [f"{r['name'] or '?'}: {r['count']} {get_raz_form(safe_int(r['count']))}" for r in rows]
    await update.effective_message.reply_text("Топ-5 Терпил:\n" + "\n".join(lines))


# -----------------------------
# Triggers Management
# -----------------------------
async def handle_triggers_list(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    db: Database = context.application.bot_data["db"]
    message = update.effective_message
    if not message:
        return

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

    lines: List[str] = ["Local Triggers:"]
    lines.extend([f"- {r['search_phrase']}" for r in local] if local else ["- (none)"])
    lines.append("\nGlobal Triggers:")
    lines.extend([f"- {r['search_phrase']}" for r in glob] if glob else ["- (none)"])
    lines.append("\nCascade Triggers:")
    lines.extend([f"- {r['search_phrase']}" for r in casc] if casc else ["- (none)"])

    await send_long_text(context, message.chat_id, "\n".join(lines), message.message_thread_id)


async def handle_remove(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    if message.from_user and message.from_user.id == BLOCKED_COMMAND_USER_ID:
        await message.reply_text("You are not allowed to use this command.")
        return

    phrase = norm_key(" ".join(context.args))
    if not phrase:
        await message.reply_text("Usage: /remove <phrase>")
        return

    db: Database = context.application.bot_data["db"]
    deleted, _ = await db.execute(
        "DELETE FROM triggers WHERE chat_id = ? AND search_phrase = ? AND is_global = ?",
        (message.chat_id, phrase, False),
    )
    await message.reply_text("Local response removed!" if deleted > 0 else "No local response found with that search phrase.")


async def handle_removeglobal(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    if not message.from_user or message.from_user.id != ADMIN_ID:
        await message.reply_text("You are not authorized.")
        return

    phrase = norm_key(" ".join(context.args))
    if not phrase:
        await message.reply_text("Usage: /removeglobal <phrase>")
        return

    db: Database = context.application.bot_data["db"]
    deleted, _ = await db.execute(
        "DELETE FROM triggers WHERE search_phrase = ? AND is_global = ?",
        (phrase, True),
    )
    await message.reply_text("Global response removed!" if deleted > 0 else "No global response found.")


async def handle_add(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    if message.from_user and message.from_user.id == BLOCKED_COMMAND_USER_ID:
        await message.reply_text("You are not allowed to use this command.")
        return
    if not message.reply_to_message:
        await message.reply_text("Reply to a message and use: /add <phrase>")
        return

    phrase = norm_key(" ".join(context.args))
    if not phrase:
        await message.reply_text("Please provide a trigger phrase.")
        return

    db: Database = context.application.bot_data["db"]
    _, cascade_exists = await check_trigger_existence(db, message.chat_id, phrase)
    if cascade_exists:
        await message.reply_text("A cascade trigger with this phrase exists.")
        return

    try:
        mr = create_my_response_from_reply(message)
    except Exception:
        await message.reply_text("Can't add this trigger")
        return

    if not myresponse_has_content(mr):
        await message.reply_text("Replied message has no supported content.")
        return

    mr.search_phrase = phrase
    ent_json = entities_to_json(mr.entities)

    exists = await db.fetchone(
        "SELECT id FROM triggers WHERE chat_id = ? AND is_global = ? AND search_phrase = ? LIMIT 1",
        (message.chat_id, False, phrase),
    )

    if exists:
        await db.execute(
            "UPDATE triggers SET response=?, file_type=?, file_id=?, file_name=?, entities=? WHERE id=?",
            (mr.response, str(mr.file_type), mr.file_id, mr.file_name, ent_json, safe_int(exists["id"])),
        )
        await message.reply_text("Response updated!")
    else:
        await db.execute(
            """INSERT INTO triggers (chat_id, search_phrase, response, file_type, file_id, file_name, entities, is_global)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?)""",
            (message.chat_id, phrase, mr.response, str(mr.file_type), mr.file_id, mr.file_name, ent_json, False),
        )
        await message.reply_text("New response added!")


async def handle_addglobal(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    if not message.from_user or message.from_user.id != ADMIN_ID:
        await message.reply_text("Unauthorized.")
        return
    if not message.reply_to_message:
        await message.reply_text("Reply to a message and use: /addglobal <phrase>")
        return

    phrase = norm_key(" ".join(context.args))
    if not phrase:
        await message.reply_text("Provide a phrase.")
        return

    db: Database = context.application.bot_data["db"]
    existing_row = await db.fetchone(
        "SELECT id FROM triggers WHERE is_global = ? AND search_phrase = ? LIMIT 1",
        (True, phrase),
    )
    existing_id = safe_int(existing_row["id"]) if existing_row else 0

    try:
        mr = create_my_response_from_reply(message)
    except Exception:
        await message.reply_text("Can't add this trigger")
        return

    if not myresponse_has_content(mr):
        await message.reply_text("No content found.")
        return

    ent_json = entities_to_json(mr.entities)
    if existing_id:
        await db.execute(
            "UPDATE triggers SET response=?, file_type=?, file_id=?, file_name=?, entities=?, chat_id=? WHERE id=?",
            (mr.response, str(mr.file_type), mr.file_id, mr.file_name, ent_json, 0, existing_id),
        )
        await message.reply_text("Global response updated!")
    else:
        await db.execute(
            """INSERT INTO triggers (chat_id, search_phrase, response, file_type, file_id, file_name, entities, is_global)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?)""",
            (0, phrase, mr.response, str(mr.file_type), mr.file_id, mr.file_name, ent_json, True),
        )
        await message.reply_text("New global response added!")


# -----------------------------
# Cascade Triggers
# -----------------------------
async def handle_addc(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    if not message.reply_to_message:
        await message.reply_text("Reply to the response message: /addc <phrase>")
        return

    phrase = norm_key(" ".join(context.args))
    if not phrase:
        await message.reply_text("Provide a phrase.")
        return

    db: Database = context.application.bot_data["db"]
    local_exists, _ = await check_trigger_existence(db, message.chat_id, phrase)
    if local_exists:
        await message.reply_text("A normal trigger exists for this phrase.")
        return

    row = await db.fetchone(
        "SELECT id FROM cascade_triggers2 WHERE chat_id = ? AND search_phrase = ?",
        (message.chat_id, phrase),
    )
    if row:
        trigger_id = safe_int(row["id"])
    else:
        _, trigger_id = await db.execute(
            "INSERT INTO cascade_triggers2 (chat_id, search_phrase) VALUES (?, ?)",
            (message.chat_id, phrase),
        )

    mr = create_my_response_from_reply(message)
    if not myresponse_has_content(mr):
        await message.reply_text("Replied message has no supported content.")
        return

    ent_json = entities_to_json(mr.entities)
    await db.execute(
        """INSERT INTO cascade_trigger_responses (cascade_trigger_id, response, file_type, file_id, file_name, entities)
           VALUES (?, ?, ?, ?, ?, ?)""",
        (trigger_id, mr.response, str(mr.file_type), mr.file_id, mr.file_name, ent_json),
    )
    await message.reply_text("Cascade trigger added!")


async def handle_removec(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    if not message.reply_to_message:
        await message.reply_text("Reply to the message you want to remove: /removec <phrase>")
        return

    phrase = norm_key(" ".join(context.args))
    if not phrase:
        await message.reply_text("Provide a phrase.")
        return

    db: Database = context.application.bot_data["db"]
    r = message.reply_to_message

    response_text = ((r.text or "") or (r.caption or "")).strip()
    file_id = ""
    for attr in ["photo", "animation", "voice", "sticker", "video", "document", "audio", "video_note"]:
        val = getattr(r, attr, None)
        if val:
            file_id = val[-1].file_id if isinstance(val, list) else val.file_id
            break

    if not response_text and not file_id:
        await message.reply_text("Target message has no content.")
        return

    row = await db.fetchone(
        "SELECT id FROM cascade_triggers2 WHERE chat_id = ? AND search_phrase = ?",
        (message.chat_id, phrase),
    )
    if not row:
        await message.reply_text("Cascade trigger not found.")
        return
    trigger_id = safe_int(row["id"])

    if file_id:
        deleted, _ = await db.execute(
            "DELETE FROM cascade_trigger_responses WHERE cascade_trigger_id = ? AND file_id = ?",
            (trigger_id, file_id),
        )
    else:
        deleted, _ = await db.execute(
            "DELETE FROM cascade_trigger_responses WHERE cascade_trigger_id = ? AND COALESCE(file_id, '') = '' AND response = ?",
            (trigger_id, response_text),
        )

    await message.reply_text(
        "Response removed from cascade." if deleted else "Matching response not found in this cascade."
    )

    row2 = await db.fetchone(
        "SELECT COUNT(*) AS c FROM cascade_trigger_responses WHERE cascade_trigger_id = ?",
        (trigger_id,),
    )
    if row2 and safe_int(row2["c"]) == 0:
        await db.execute("DELETE FROM cascade_triggers2 WHERE id = ?", (trigger_id,))
        await message.reply_text("Cascade trigger deleted as it is now empty.")


# -----------------------------
# Timezone Commands
# -----------------------------
async def time_add(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    location = " ".join(context.args).strip()
    if not location:
        await message.reply_text("Usage: /addlocation <Location>")
        return

    try:
        _ = get_current_time_for_location(location)
    except Exception:
        await message.reply_text("Location unavailable.")
        return

    db: Database = context.application.bot_data["db"]
    try:
        await db.execute("INSERT INTO timezones (chatID, location) VALUES (?, ?)", (message.chat_id, location))
    except aiosqlite.Error:
        await message.reply_text("Location already added.")
        return

    rows = await db.fetchall("SELECT location FROM timezones WHERE chatID = ?", (message.chat_id,))
    locations = [r["location"] for r in rows]
    await message.reply_text(f"Added! Current locations: {', '.join(locations)}")


async def time_remove(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    location = " ".join(context.args).strip()
    if not location:
        await message.reply_text("Usage: /removelocation <Location>")
        return

    db: Database = context.application.bot_data["db"]
    deleted, _ = await db.execute("DELETE FROM timezones WHERE chatID = ? AND location = ?", (message.chat_id, location))
    await message.reply_text(f"Removed '{location}'" if deleted else "Location not found.")


async def add_or_update_alias(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message:
        return
    args = " ".join(context.args)
    parts = args.split("-", 1)
    if len(parts) != 2:
        await message.reply_text("Usage: /alias Location - Alias")
        return
    location = parts[0].strip()
    alias = parts[1].strip()

    db: Database = context.application.bot_data["db"]
    row = await db.fetchone(
        "SELECT 1 AS x FROM alias WHERE chatID = ? AND location = ? LIMIT 1",
        (message.chat_id, location),
    )
    if row:
        await db.execute("UPDATE alias SET alias = ? WHERE chatID = ? AND location = ?", (alias, message.chat_id, location))
    else:
        await db.execute("INSERT INTO alias (chatID, location, alias) VALUES (?, ?, ?)", (message.chat_id, location, alias))
    await message.reply_text(f"Alias set: {location} -> {alias}")


async def reset_message(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    db: Database = context.application.bot_data["db"]
    chat_id = update.effective_chat.id
    await db.execute("DELETE FROM messagelist WHERE chatID = ?", (chat_id,))
    await update_time_message_for_chat(db, context, chat_id)


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
        try:
            now = get_current_time_for_location(r["location"])
            locs.append((r["location"], r["display_location"], now))
        except Exception:
            continue

    if not locs:
        return

    locs.sort(key=lambda x: (x[2].date().toordinal(), x[2].hour, x[2].minute))
    text = "\n".join([f"{display} {dt.strftime('%H:%M')}" for _, display, dt in locs])

    row = await db.fetchone("SELECT messageID FROM messagelist WHERE chatID = ?", (chat_id,))
    bot = context.bot

    if row is None:
        try:
            sent = await bot.send_message(chat_id=chat_id, text=text)
            await db.execute("INSERT INTO messagelist (chatID, messageID) VALUES (?, ?)", (chat_id, sent.message_id))
        except Forbidden:
            pass
        return

    msg_id = safe_int(row["messageID"])
    try:
        await bot.edit_message_text(chat_id=chat_id, message_id=msg_id, text=text)
    except BadRequest as e:
        if "message to edit not found" in str(e).lower() or "message can't be edited" in str(e).lower():
            await db.execute("DELETE FROM messagelist WHERE chatID = ?", (chat_id,))
            try:
                sent = await bot.send_message(chat_id=chat_id, text=text)
                await db.execute("INSERT INTO messagelist (chatID, messageID) VALUES (?, ?)", (chat_id, sent.message_id))
            except Forbidden:
                pass
    except Forbidden:
        pass


async def update_time_job(context: ContextTypes.DEFAULT_TYPE) -> None:
    db: Database = context.application.bot_data["db"]
    chat_ids = await db.get_all_active_chat_ids()
    for cid in chat_ids:
        await update_time_message_for_chat(db, context, cid)


# -----------------------------
# Special behaviors
# -----------------------------
async def handle_new_member(context: ContextTypes.DEFAULT_TYPE, message: Message) -> None:
    sticker_set_name = "privetcivpack_by_fStikBot"
    try:
        stset = await context.bot.get_sticker_set(name=sticker_set_name)
        if stset.stickers:
            st = random.choice(stset.stickers)
            kwargs: Dict[str, Any] = {"chat_id": message.chat_id, "sticker": st.file_id}
            if message.message_thread_id is not None:
                kwargs["message_thread_id"] = message.message_thread_id
            await _call_with_thread_fallback(context.bot.send_sticker, **kwargs)
    except Exception as e:
        logger.info("handle_new_member failed: %s", e)


async def handle_new_member_update(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message or not message.new_chat_members:
        return
    await handle_new_member(context, message)


async def maybe_send_mention_memes(context: ContextTypes.DEFAULT_TYPE, message: Message) -> bool:
    text = message.text or ""
    if not text:
        return False

    try:
        current_moscow = get_current_time_for_location("Moscow")
    except Exception:
        current_moscow = datetime.now(timezone.utc)

    # Meme 1
    if message.chat_id in (-1001245934322, -1001390115843) and "@Porky8888" in text:
        la = datetime.now(ZoneInfo("America/Los_Angeles"))
        if not is_time_between(la, 2, 7) or random.random() < 0.5:
            return False
        caption = f"Машталер в {la.hour} ночи" if is_time_between(la, 2, 4) else f"Машталер в {la.hour} утра"
        await message.reply_photo(
            photo="AgACAgQAAx0Cc2pGjQACAUBlssL7rSKP4mmzMMYeORKjAS3LOAACHMIxGzznmFF5Spk5RRTfbwEAAwIAA3gAAzQE",
            caption=caption,
        )
        return True

    # Meme 2
    if message.chat_id == -1001970411651 and "@vincenitycarter" in text and is_time_between_19_and_8(current_moscow):
        fid = "AgACAgQAAx0Cc2pGjQACAX9ltZ3416cTOKI_-1Jp1wXzAVCLygACG74xGwkasVEOQZYuKQ4abQEAAwIAA3kAAzUE"
        if random.random() < 0.5:
            fid = "AgACAgIAAx0Cc2pGjQACAnVmeHhbXkkqgeg_DNEW1dChwB3BYQACuNoxG2g9yUsZaxbgiGFD_wEAAwIAA3kAAzUE"
        caption = (
            f"Сегодня, в {current_moscow.strftime('%H:%M')}, Яков Андреев был найден спящим в своей квартире. "
            f"Приносим соболезнования всем его тиммейтам"
        )
        await message.reply_photo(photo=fid, caption=caption)
        return True

    # Meme 3
    if message.chat_id in (-1002245157577, -1001936344717) and "@KelThuzad" in text:
        ny = datetime.now(ZoneInfo("America/New_York"))
        if not is_time_between(ny, 2, 7):
            return False
        if message.chat_id == -1002245157577 and random.random() < 0.3:
            return False
        caption = f"Кел в {ny.hour} ночи" if is_time_between(ny, 2, 4) else f"Кел в {ny.hour} утра"
        await message.reply_photo(
            photo="AgACAgQAAx0Cc2pGjQACArNm0PVZDzYsYwqBhiOBkCD4rCu8cQAC-78xGxt-iFJZyKNkTiV9hQEAAwIAA3gAAzUE",
            caption=caption,
        )
        return True

    return False


# -----------------------------
# Main Message Handler (Trigger Logic)
# -----------------------------
async def handle_text_logic(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    message = update.effective_message
    if not message or not message.from_user:
        return

    # 1. Terpet check
    txt_lower = (message.text or "").lower()
    if message.chat_id == -1001390115843 and "терпеть" in txt_lower:
        db = context.application.bot_data["db"]
        await increment_terpet(db, context, message)

    # 2. Keyword Cooldown Check
    target_chat_id = -1002245157577
    keyword = "рейдодроч"
    txt_for_keyword = message.text or message.caption or ""
    if message.chat_id == target_chat_id and norm_key(txt_for_keyword) == norm_key(keyword):
        ok = await check_and_update_last_keyword(target_chat_id, keyword)
        if not ok:
            return

    # 3. Mention Memes
    if await maybe_send_mention_memes(context, message):
        return

    # 4. DB Triggers
    if not (message.text or message.caption):
        return

    db: Database = context.application.bot_data["db"]
    received_key = norm_key(message.text or message.caption or "")
    if not received_key:
        return

    # Standard triggers: Local then Global
    row = await db.fetchone(
        """SELECT id, search_phrase, response, file_type, file_id, file_name, entities
           FROM triggers
           WHERE chat_id = ? AND is_global = ? AND search_phrase = ?
           LIMIT 1""",
        (message.chat_id, False, received_key),
    )
    if not row:
        row = await db.fetchone(
            """SELECT id, search_phrase, response, file_type, file_id, file_name, entities
               FROM triggers
               WHERE is_global = ? AND search_phrase = ?
               LIMIT 1""",
            (True, received_key),
        )

    if row:
        resp = MyResponse(
            id=safe_int(row["id"]),
            search_phrase=row["search_phrase"] or "",
            response=row["response"] or "",
            file_type=FileType(row["file_type"] or ""),
            file_id=row["file_id"] or "",
            file_name=row["file_name"] or "",
            entities=entities_from_json(row["entities"] or ""),
        )
        await send_trigger_response(context, message, resp, reply_to=True)

    # Cascade triggers (FIXED earlier: rowid + safe_int)
    rows = await db.fetchall(
        """
        SELECT
          ctr.rowid AS rid,
          ctr.response, ctr.file_type, ctr.file_id, ctr.file_name, ctr.entities
        FROM cascade_triggers2 ct
        JOIN cascade_trigger_responses ctr ON ct.id = ctr.cascade_trigger_id
        WHERE ct.chat_id = ? AND ct.search_phrase = ?
        ORDER BY ctr.rowid ASC
        """,
        (message.chat_id, received_key),
    )
    for r in rows:
        resp = MyResponse(
            id=safe_int(r["rid"]),
            response=r["response"] or "",
            file_type=FileType(r["file_type"] or ""),
            file_id=r["file_id"] or "",
            file_name=r["file_name"] or "",
            entities=entities_from_json(r["entities"] or ""),
        )
        await send_trigger_response(context, message, resp, reply_to=False)


# -----------------------------
# PTB Error Handler
# -----------------------------
async def error_handler(update: object, context: ContextTypes.DEFAULT_TYPE) -> None:
    logger.exception("Unhandled exception while processing an update", exc_info=context.error)


# -----------------------------
# Startup / Shutdown
# -----------------------------
async def on_startup(app: Application) -> None:
    db = Database(DB_PATH)
    await db.connect()
    await db.init_schema()
    app.bot_data["db"] = db
    app.bot_data["allowed_users"] = await load_allowed_users(ALLOWED_USERS_PATH)
    try:
        app.bot_data["jitsi_app_secret"] = read_app_secret(APP_SECRET_PATH)
    except FileNotFoundError:
        app.bot_data["jitsi_app_secret"] = ""
        logger.warning("Jitsi app secret not found at %s", APP_SECRET_PATH)

    chat_ids = await db.get_all_active_chat_ids()

    class _TmpCtx:
        def __init__(self, application: Application) -> None:
            self.application = application
            self.bot = application.bot

    tmp_ctx = _TmpCtx(app)

    for cid in chat_ids:
        await update_time_message_for_chat(db, tmp_ctx, cid)  # type: ignore

    if app.job_queue:
        app.job_queue.run_repeating(update_time_job, interval=30, first=30)
    else:
        logger.warning("JobQueue not available.")
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
    try:
        token = read_bot_token(BOT_TOKEN_PATH)
    except FileNotFoundError:
        logger.error(
            "Bot token not found. Set BOT_TOKEN/TELEGRAM_BOT_TOKEN or create %s.",
            BOT_TOKEN_PATH,
        )
        sys.exit(1)
    if not token:
        logger.error(
            "Bot token is empty. Set BOT_TOKEN/TELEGRAM_BOT_TOKEN or update %s.",
            BOT_TOKEN_PATH,
        )
        sys.exit(1)

    # -- SECURITY: Apply Redaction Filter --
    root_logger = logging.getLogger()
    redacter = TokenRedactionFilter(token)
    for handler in root_logger.handlers:
        handler.addFilter(redacter)

    logging.getLogger("httpx").setLevel(logging.WARNING)
    logging.getLogger("httpcore").setLevel(logging.WARNING)
    # --------------------------------------

    app = (
        Application.builder()
        .token(token)
        .post_init(on_startup)
        .post_shutdown(on_shutdown)
        .build()
    )

    app.add_error_handler(error_handler)

    # Commands
    app.add_handler(CommandHandler("chatid", handle_chatid))
    app.add_handler(CommandHandler("getlink", handle_getlink))
    app.add_handler(CommandHandler("generateqr", handle_generate_qr))
    app.add_handler(CommandHandler("generatebar", handle_generate_barcode))
    app.add_handler(CommandHandler("samplesize", handle_samplesize))
    app.add_handler(CommandHandler("terpet", handle_terpet_command))
    app.add_handler(CommandHandler("topterpil", handle_topterpil))
    app.add_handler(CommandHandler("allow_create", handle_allow_create))
    app.add_handler(CommandHandler("ban_create", handle_ban_create))
    app.add_handler(CommandHandler("create_call", handle_create_call))

    # Triggers / Timezone commands
    app.add_handler(CommandHandler("triggers", handle_triggers_list))
    app.add_handler(CommandHandler("add", handle_add))
    app.add_handler(CommandHandler("remove", handle_remove))
    app.add_handler(CommandHandler("addglobal", handle_addglobal))
    app.add_handler(CommandHandler("removeglobal", handle_removeglobal))
    app.add_handler(CommandHandler("addc", handle_addc))
    app.add_handler(CommandHandler("removec", handle_removec))

    app.add_handler(CommandHandler("addlocation", time_add))
    app.add_handler(CommandHandler("removelocation", time_remove))
    app.add_handler(CommandHandler("alias", add_or_update_alias))
    app.add_handler(CommandHandler("resetmessage", reset_message))

    # Rolls
    app.add_handler(CommandHandler("roll", partial(handle_roll, sides=100)))
    app.add_handler(CommandHandler("roll20", partial(handle_roll, sides=20)))
    app.add_handler(CommandHandler("roll12", partial(handle_roll, sides=12)))
    app.add_handler(CommandHandler("roll10", partial(handle_roll, sides=10)))
    app.add_handler(CommandHandler("roll8", partial(handle_roll, sides=8)))
    app.add_handler(CommandHandler("roll6", partial(handle_roll, sides=6)))
    app.add_handler(CommandHandler("roll4", partial(handle_roll, sides=4)))

    # Fallback for triggers and other logic (excluding commands)
    app.add_handler(MessageHandler(filters.StatusUpdate.NEW_CHAT_MEMBERS, handle_new_member_update))

    app.add_handler(
        MessageHandler(
            (filters.TEXT | filters.CAPTION) & filters.ChatType.GROUPS,
            handle_text_logic,
        )
    )

    # Private chat fallback
    async def private_echo(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
        m = update.effective_message
        if not m:
            return
        forward_from_id: Optional[int] = None
        forward_sender_hidden = False

        # PTB 20+: forward_from/forward_sender_name may be absent for many forwarded messages.
        # Prefer forward_origin if available, keep legacy fields for backward compatibility.
        origin = getattr(m, "forward_origin", None)
        if origin is not None:
            sender_user = getattr(origin, "sender_user", None)
            sender_user_id = getattr(sender_user, "id", None)
            if sender_user_id:
                forward_from_id = sender_user_id
            elif getattr(origin, "sender_user_name", None):
                forward_sender_hidden = True
            elif getattr(origin, "type", "") == "hidden_user":
                forward_sender_hidden = True

        legacy_forward_from = getattr(m, "forward_from", None)
        if legacy_forward_from and getattr(legacy_forward_from, "id", None):
            forward_from_id = legacy_forward_from.id

        if getattr(m, "forward_sender_name", None):
            forward_sender_hidden = True

        if forward_from_id is not None:
            await m.reply_html(f"<b>Message forwarded from User ID: </b> <code>{forward_from_id}</code>")
        elif forward_sender_hidden:
            await m.reply_html("<b>Sorry, this user's ID is hidden</b>")
        elif m.from_user:
            await m.reply_html(f"<b>Your User ID:</b> <code>{m.from_user.id}</code>")

    app.add_handler(MessageHandler(filters.ChatType.PRIVATE, private_echo))

    app.run_polling(allowed_updates=Update.ALL_TYPES)


if __name__ == "__main__":
    main()
