# Dockerfile — сборка образа для Koyeb.
#
# Два этапа:
#   1. golang: собираем статический бинарник booksender;
#   2. alpine: копируем бинарник и корневые сертификаты
#      (нужны для HTTPS к Telegram и TLS к SMTP).
#
# Зачем: итоговый образ около 10 МБ, стартует за секунду,
# Koyeb собирает его сам из git репозитория.

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /booksender .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /booksender /booksender
EXPOSE 8000
CMD ["/booksender"]
