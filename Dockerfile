FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /caduceus ./cmd/caduceus

FROM scratch
COPY --from=build /caduceus /caduceus
ENV CADUCEUS_LISTEN=:8026 CADUCEUS_DATA_DIR=/data TMPDIR=/data
VOLUME /data
EXPOSE 8026
ENTRYPOINT ["/caduceus"]
