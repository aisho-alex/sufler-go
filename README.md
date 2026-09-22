# sufler-go

Go-версия [sufler-live](../sufler-live): ИИ-суфлёр в реальном времени — слушает
системный звук и микрофон, распознаёт речь локально (whisper.cpp на CUDA),
анализирует разговор через LLM и показывает подсказки в GTK-оверлее.
Важное (имена, цифры, договорённости) сохраняется в SQLite.

Переписан с Python ради одного исполняемого файла и расхода памяти
(см. «Замеры»). Конфиг, промпты, схема БД и CLI совместимы с Python-версией.

## Замеры (RTX 4080 Laptop, large-v3-turbo)

| Метрика | Python (faster-whisper) | Go (whisper.cpp) |
|---|---|---|
| Пиковый RSS процесса | 1428 МБ | **265 МБ** (5.4× меньше) |
| RTF инференса (прогретый) | 0.019 | **0.012** |
| Загрузка модели | 3.2 с | **1.2 с** |
| VAD-сегментация | эталон | бит-в-байт паритет (Δ=0 сэмплов) |

## Архитектура

```
PipeWire ── pw-dump (дефолты) + ffmpeg -f pulse (мик/монитор, супервизор)
   → Silero VAD v5 (onnxruntime, models/silero_*.onnx)
   → whisper.cpp large-v3-turbo (CUDA, статически вшит в бинарь)
   → SQLite (segments/hints/notes/qa) + шина событий
   → LLM qwen (neuraldeep, OpenAI API, дебаунс ≥N слов/секунд, guided_json)
   → GTK3-оверлей (gotk3) или консоль
```

## Требования

Сборка: Go ≥ 1.25, cmake, CUDA toolkit (nvcc 12), pkg-config, libgtk-3-dev,
ffmpeg, pipewire (pw-dump).

Рядом с бинарем при запуске:

- `lib/onnxruntime.so` — сборка onnxruntime ≥ 1.29
  (качается с github.com/microsoft/onnxruntime/releases, файл
  `libonnxruntime.so.1.29.1` → переименовать);
- `models/` — silero_v5 (в репе) + `ggml-large-v3-turbo.bin` (`make model`);
- путь можно переопределить: `SUFLER_ONNXRUNTIME_LIB`, `SUFLER_MODELS`.

## Сборка

```bash
git clone --recurse-submodules <repo>
cd sufler-go
make whisper-build   # статические libwhisper+ggml с CUDA (arch 89) + merge
make model           # скачать ggml-large-v3-turbo.bin (~1.6 ГБ)
make build           # bin/sufler (~215 МБ: внутри CUDA-кубины sm_89)
make check           # самопроверка конфига и БД
make test            # юнит-тесты (VAD-паритет требует живого окружения)
```

Нюансы статики: все ggml/whisper архивы склеиваются в один `lib/libwhisper.a`
(`make whisper-merge`) — однопроходный линкер иначе не разрешает зависимости
ggml→cuda; CUDA-библиотеки (cublas/cudart) линкуются динамически из системы,
onnxruntime подгружается через dlopen. В `third_party/whisper.cpp` патчена
одна строка cgo LDFLAGS биндинга (см. `git diff` в сабмодуле).

## Конфигурация

Как в Python-версии: `config.yaml`, `.env`
(`NEURALDEEP_API_KEY`, `NEURALDEEP_BASE_URL`, `SUFLER_MODEL`),
`prompts/default.txt`, схема SQLite совпадает байт-в-байт.

## Запуск

```bash
./bin/sufler                 # оверлей (старт захвата — кнопка ▶)
./bin/sufler --no-ui         # консоль: текст — заметка, '?вопрос' — спросить LLM
./bin/sufler --no-mic        # только системный звук
./bin/sufler --list-devices
./bin/sufler --model base    # другая модель whisper
```

Оверлей: перетаскивается мышью, ▯ сворачивает в пилюлю, ✕ — выход.
Строка ввода: Enter — вопрос LLM, Ctrl+Enter — заметка.

## Windows (CPU, без CUDA)

Собирается в GitHub Actions (`.github/workflows/windows-build.yml`, msys2):
whisper.cpp CPU static + GTK3 + onnxruntime, упаковывается в
`sufler-windows.zip` (артефакт каждого прогона; на тег — release).

```text
1. Скачать sufler-windows.zip из Actions (или из Releases)
2. Распаковать; в bin\ создайте .env:  NEURALDEEP_API_KEY=<ключ>
3. bin\download-model.cmd                          # ggml-small.bin (~466 МБ)
   # или: powershell -ExecutionPolicy Bypass -File bin\download-model.ps1
4. bin\sufler.exe                                  # оверлей (▶ — старт)
   bin\sufler.exe --no-ui --list-devices           # консоль/устройства
```

- Захват — WASAPI: микрофон с дефолтного capture-устройства, системный звук —
  loopback с дефолтного render-устройства (смена устройства подхватывается
  раз в 10 с). WASAPI сам конвертит mix format в 48кГц моно, дальше 16кГц.
- Модель по умолчанию — `small` (`config.yaml`→`asr.model`); на CPU `small`
  даёт RTF ≈ 0.1–0.3, `large-v3-turbo` — ≈ 0.5 и больше.
- Требуется CPU с AVX2/FMA/F16C (Intel Haswell 2013+ / AMD Excavator+).
- GTK3-рантайм и onnxruntime.dll идут в комплекте (папки `lib/`, `share/`,
  `etc/`); интернет нужен только для модели и LLM.

## Отличия от Python-версии

- Захват — только через ffmpeg-pulse (PortAudio/sounddevice не портирован;
  в auto-режиме питон для монитора и так использовал ffmpeg).
- HTTP к LLM — принудительно HTTP/1.1: h2 у api.neuraldeep.ru флакует
  (ALPN соглашается, ответа нет).
- Оверлей на GTK3/X11: keep-above и перетаскивание как в Qt; на Wayland
  окно живёт через XWayland (у Python-Qt те же ограничения).

## Известные ограничения

- Бинарь ~215 МБ: вшиты CUDA-кубины под sm_89 (RTX 40xx). Другая карта —
  пересборка `make whisper-build` с нужным `CMAKE_CUDA_ARCHITECTURES`.
- Windows-сборка — CPU-only; заметно медленнее CUDA-версии (см. выше).
- При выводе звука в колонки микрофон ловит эхо — используйте наушники.
