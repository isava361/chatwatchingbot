package main

import (
tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
"strings"
"os"
"log"
"fmt"
"github.com/fsnotify/fsnotify"
"strconv"
"github.com/skip2/go-qrcode"
"github.com/boombuler/barcode"
"github.com/boombuler/barcode/code128"
"image/png"
)
