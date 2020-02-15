# Compile
FROM golang:1.13-alpine AS compiler

RUN apk add --no-cache git make

# enable go modules
WORKDIR /src
COPY . .

RUN CGO_ENABLED=0 make build


# Build image
FROM scratch

COPY --from=compiler /src/sower /sower
ENTRYPOINT [ "/sower" ]