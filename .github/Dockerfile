# Compile
FROM golang:1.13-alpine AS compiler

RUN apk add --no-cache git make

# enable go modules
WORKDIR /workdir
COPY . .

# do not worry about downloading dependency, sower will fix this.
RUN CGO_ENABLED=0 make build


# Build image
FROM scratch

COPY --from=compiler /workdir/sower /sower
ENTRYPOINT [ "/sower" ]