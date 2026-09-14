FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /booksender .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /booksender /booksender
EXPOSE 8000
CMD ["/booksender"]
