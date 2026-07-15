FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/authelia-telegram-notifier .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/authelia-telegram-notifier /authelia-telegram-notifier
ENTRYPOINT ["/authelia-telegram-notifier"]
