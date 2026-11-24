# FFmpeg / ffprobe 9.1

Статические сборки Martin Riedl: https://ffmpeg.martin-riedl.de/ . В приложение включены universal Mach-O файлы, полученные объединением неизменённого кода Intel/Apple Silicon через lipo; подпись заменена локальной ad-hoc подписью.

Intel (amd64), release 9.1, 1795212433_9.1:
- https://ffmpeg.martin-riedl.de/download/macos/amd64/1795212433_9.1/ffmpeg.zip
- SHA256: 35b3d82cccc2c1d644d49d15f5e57eb913a08ef68f798042c90eecec62c51f1e
- https://ffmpeg.martin-riedl.de/download/macos/amd64/1795212433_9.1/ffprobe.zip
- SHA256: 41ca7b7cba7992e33f1da3f1bf54db8ec103a4d387b1bfac3162062e8b1074ad

Apple Silicon (arm64), release 9.1, 1795207104_9.1:
- https://ffmpeg.martin-riedl.de/download/macos/arm64/1795207104_9.1/ffmpeg.zip
- SHA256: 1f66e9a690a7f1c9e60b012379f8869c11818d1797f45b21dbb2f2564ba61ae1
- https://ffmpeg.martin-riedl.de/download/macos/arm64/1795207104_9.1/ffprobe.zip
- SHA256: 6ef2df5f575042923a28537ab37adf936f28bf3e7b85de23a1447ed447ec1dbd

Для каждого ZIP опубликована контрольная сумма по тому же URL с суффиксом `.sha256`.

FFmpeg распространяется под GPLv3 или более поздней версией. Текст лицензии прилагается в `FFmpeg-LICENSE.txt`. Версия и полная конфигурация сборки для каждой архитектуры — в `FFmpeg-build-info.txt`. Исходный код FFmpeg 9.1: https://ffmpeg.org/releases/ffmpeg-9.1.tar.xz ; сценарий сборки и исходники внешних зависимостей: https://git.martin-riedl.de/ffmpeg/build-script . Официальный проект: https://ffmpeg.org/ .
