#!/bin/bash
# Сборка sufler-windows.zip из артефактов CI-сборки (запускается в msys2).
# Использование: package-windows.sh <workspace>
set -e
WS="$1"
DIST="$WS/dist"
LIB="$WS/lib"
BIN="$DIST/bin"

rm -rf "$DIST"
mkdir -p "$BIN" "$DIST/models"

cp "$WS/bin/sufler.exe" "$BIN/"
cp "$WS/models/silero_encoder_v5.onnx" "$WS/models/silero_decoder_v5.onnx" "$DIST/models/"
cp -r "$WS/prompts" "$DIST/prompts"
# config.yaml как в репе, но модель под CPU — small (первое вхождение "  model:")
sed -e '0,/^  model: /s/^  model: .*/  model: small              # tiny | base | small | medium | large-v3-turbo/' \
    -e '0,/^  device: /s/^  device: .*/  device: cpu/' \
    "$WS/config.yaml" > "$DIST/config.yaml"

# onnxruntime + VC++ рантайм (его зависимости) — рядом с exe:
# Windows ищет зависимости DLL в каталоге exe, поэтому всё кладём в bin/.
cp "$LIB/onnxruntime.dll" "$BIN/"

VS_CRT=$(ls -d "/c/Program Files/Microsoft Visual Studio/2022/"*/VC/Redist/MSVC/*/x64/Microsoft.VC143.CRT 2>/dev/null | tail -1 || true)
copy_crt() {
    local dll="$1"
    for src in /c/Windows/System32 "$VS_CRT"; do
        if [ -n "$src" ] && [ -f "$src/$dll" ]; then
            cp "$src/$dll" "$BIN/"
            return 0
        fi
    done
    echo "ПРЕДУПРЕЖДЕНИЕ: не найден $dll"
}
for dll in MSVCP140.dll MSVCP140_1.dll MSVCP140_2.dll VCRUNTIME140.dll VCRUNTIME140_1.dll concrt140.dll; do
    copy_crt "$dll"
done

# GTK и прочие mingw-DLL: рекурсивно от exe — тоже в bin/
collect() {
    local dll="$1"
    local target="/mingw64/bin/$dll"
    [ -f "$target" ] || return 0
    [ -f "$BIN/$dll" ] && return 0
    cp "$target" "$BIN/"
    for dep in $(objdump -p "$target" | grep 'DLL Name' | sed 's/.*DLL Name: \(.*\)/\1/' | tr -d '\r'); do
        collect "$dep"
    done
}
for dep in $(objdump -p "$WS/bin/sufler.exe" | grep 'DLL Name' | sed 's/.*DLL Name: \(.*\)/\1/' | tr -d '\r'); do
    collect "$dep"
done

# Данные GTK: msys2-раскладка — DLL в bin/, share/etc рядом с bin/
mkdir -p "$DIST/etc" "$DIST/share/glib-2.0"
cp -r /mingw64/etc/fonts "$DIST/etc/fonts"
cp -r /mingw64/share/glib-2.0/schemas "$DIST/share/glib-2.0/schemas"
mkdir -p "$DIST/lib/gdk-pixbuf-2.0/2.10.0"
cp -r /mingw64/lib/gdk-pixbuf-2.0/2.10.0/loaders "$DIST/lib/gdk-pixbuf-2.0/2.10.0/loaders" 2>/dev/null || true
cp -r /mingw64/share/icons/Adwaita "$DIST/share/icons/Adwaita" 2>/dev/null || true
cp -r /mingw64/share/themes/Windows10 "$DIST/share/themes/Windows10" 2>/dev/null || true

cat > "$DIST/sufler.cmd" <<'EOF'
@echo off
cd /d "%~dp0"
start "" "bin\sufler.exe"
EOF

cat > "$DIST/README-WINDOWS.txt" <<'EOF'
sufler — ИИ-суфлёр (Windows, CPU)

Запуск (из корня пакета, где лежит этот файл):
  sufler.cmd                # оверлей без консоли (рекомендуется)
  bin\sufler.exe            # оверлей (кнопка ▶ — старт захвата)
  bin\sufler.exe --no-ui    # консоль: текст — заметка, '?вопрос' — вопрос LLM
  bin\sufler.exe --list-devices
  bin\sufler.exe --check

Первый шаг: скачайте модель whisper (по умолчанию small, ~466 МБ):
  bin\download-model.cmd
  (или: powershell -ExecutionPolicy Bypass -File bin\download-model.ps1)

Ключ LLM: в корне пакета (рядом с config.yaml) создайте .env:
  NEURALDEEP_API_KEY=<ключ>

Модель побольше (больше, но медленнее на CPU — правьте config.yaml asr.model):
  bin\download-model.cmd large-v3-turbo

Данные (транскрипты, подсказки): data\sufler.db и data\sufler.log
EOF

cat > "$BIN/download-model.cmd" <<'EOF'
@echo off
setlocal
set MODEL=%~1
if "%MODEL%"=="" set MODEL=small
set URL=https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-%MODEL%.bin
set DST=%~dp0..\models\ggml-%MODEL%.bin
if not exist "%~dp0..\models" mkdir "%~dp0..\models"
echo Downloading %URL%
curl.exe -L -C - -o "%DST%.part" "%URL%"
if errorlevel 1 (
  echo Download failed. Check curl.exe availability and network.
  exit /b 1
)
move /y "%DST%.part" "%DST%" >nul
echo Done: %DST%
EOF

cat > "$BIN/download-model.ps1" <<'EOF'
# Запуск: powershell -ExecutionPolicy Bypass -File download-model.ps1
param([string]$Model = "small")
$ErrorActionPreference = "Stop"
$url = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-$Model.bin"
$dst = Join-Path (Split-Path $PSScriptRoot -Parent) "models\ggml-$Model.bin"
New-Item -ItemType Directory -Force (Split-Path $dst) | Out-Null
Write-Host "Скачиваю $url"
Invoke-WebRequest -Uri $url -OutFile "$dst.part"
Move-Item -Force "$dst.part" $dst
Write-Host "Готово: $dst"
EOF

cd "$DIST"
zip -qr "$WS/sufler-windows.zip" .
echo "sufler-windows.zip готов"
