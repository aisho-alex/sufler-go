#!/bin/bash
# Сборка sufler-windows.zip из артефактов CI-сборки (запускается в msys2).
# Использование: package-windows.sh <workspace>
set -e
WS="$1"
DIST="$WS/dist"
LIB="$WS/lib"

rm -rf "$DIST"
mkdir -p "$DIST/bin" "$DIST/lib" "$DIST/models"

cp "$WS/bin/sufler.exe" "$DIST/bin/"
cp "$LIB/onnxruntime.dll" "$DIST/lib/"
cp "$WS/models/silero_encoder_v5.onnx" "$WS/models/silero_decoder_v5.onnx" "$DIST/models/"
cp -r "$WS/prompts" "$DIST/prompts"

# config.yaml как в репе, но модель под CPU — small (первое вхождение "  model:")
sed '0,/^  model: /s//  model: small              /' "$WS/config.yaml" > "$DIST/config.yaml"

# Рекурсивно собираем mingw-DLL-зависимости через objdump.
collect() {
    local dll="$1"
    local target="/mingw64/bin/$dll"
    [ -f "$target" ] || return 0
    [ -f "$DIST/$dll" ] && return 0
    cp "$target" "$DIST/"
    for dep in $(objdump -p "$target" | grep 'DLL Name' | sed 's/.*DLL Name: \(.*\)/\1/' | tr -d '\r'); do
        collect "$dep"
    done
}

collect sufler.exe.dependencies_start
for dep in $(objdump -p "$WS/bin/sufler.exe" | grep 'DLL Name' | sed 's/.*DLL Name: \(.*\)/\1/' | tr -d '\r'); do
    collect "$dep"
done
rm -f "$DIST/sufler.exe.dependencies_start"

# Конфиги шрифтов и glib-схемы, иначе GTK ругается при старте.
mkdir -p "$DIST/etc" "$DIST/share/glib-2.0"
cp -r /mingw64/etc/fonts "$DIST/etc/fonts"
cp -r /mingw64/share/glib-2.0/schemas "$DIST/share/glib-2.0/schemas"
mkdir -p "$DIST/lib/gdk-pixbuf-2.0/2.10.0"
cp -r /mingw64/lib/gdk-pixbuf-2.0/2.10.0/loaders "$DIST/lib/gdk-pixbuf-2.0/2.10.0/loaders" 2>/dev/null || true
cp -r /mingw64/share/icons/Adwaita "$DIST/share/icons/Adwaita" 2>/dev/null || true
cp -r /mingw64/share/themes/Windows10 "$DIST/share/themes/Windows10" 2>/dev/null || true

cat > "$DIST/README-WINDOWS.txt" <<'EOF'
sufler — ИИ-суфлёр (Windows, CPU)

Запуск:
  cd bin
  sufler.exe            # оверлей (кнопка ▶ — старт захвата)
  sufler.exe --no-ui    # консоль: текст — заметка, '?вопрос' — вопрос LLM
  sufler.exe --list-devices

Первый шаг: скачайте модель whisper (по умолчанию small, ~466 МБ):
  powershell -File download-model.ps1

Ключ LLM: рядом с bin/ создайте .env:
  NEURALDEEP_API_KEY=<ключ>

Модель побольше (large-v3-turbo, ~1.6 ГБ, медленнее на CPU):
  powershell -File download-model.ps1 -Model large-v3-turbo
EOF

sed -i 's/Модель побольше (large-v3-turbo, ~1.6 ГБ, медленнее на CPU):/Модель побольше (больше, но медленнее на CPU — правьте config.yaml asr.model):/' "$DIST/README-WINDOWS.txt"

cat > "$DIST/bin/download-model.ps1" <<'EOF'
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
