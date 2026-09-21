# sufler-go

Go-версия [sufler-live](../sufler-live): ИИ-суфлёр в реальном времени — слушает
системный звук и микрофон, распознаёт речь локально (whisper.cpp на GPU),
анализирует разговор через LLM и показывает подсказки в GTK-оверлее.
Важное (имена, цифры, договорённости) сохраняется в SQLite.

Цели переписывания: один исполняемый файл и меньший расход памяти
(эталон — Python-версия в `sufler-live`).

## Статус

Переписывание в процессе:

- [x] Фаза 0 — каркас: config, SQLite, шина событий, лог
- [ ] Фаза 1 — захват аудио (ffmpeg/pulse, супервизор)
- [ ] Фаза 2 — VAD (Silero ONNX)
- [ ] Фаза 3 — ASR (whisper.cpp, CUDA)
- [ ] Фаза 4 — LLM-анализатор
- [ ] Фаза 5 — CLI, консольный режим
- [ ] Фаза 6 — GTK-оверлей
- [ ] Фаза 7 — паритет, замеры, пакеджинг

## Сборка

```bash
make build   # bin/sufler (фаза 0 — без cgo, статический)
make check   # самопроверка конфига и БД
```

## Конфигурация

Совместима с Python-версией: `config.yaml`, `prompts/default.txt`, `.env`
(`NEURALDEEP_API_KEY`, `NEURALDEEP_BASE_URL`, `SUFLER_MODEL`), схема SQLite.

```bash
cp ../sufler-live/.env .env   # ключ LLM
```

## Запуск

```bash
./bin/sufler --check      # проверка конфига и БД
./bin/sufler --version
```
