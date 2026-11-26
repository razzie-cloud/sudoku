FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/sudoku ./cmd/sudoku

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/sudoku /usr/local/bin/sudoku
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/licenses/sudoku/
COPY licenses/ /usr/share/licenses/sudoku/licenses/
USER 10001:10001
ENV SERVICE_PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s CMD ["/usr/local/bin/sudoku", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/sudoku"]
