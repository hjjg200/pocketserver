
# https://github.com/ish-app/ish/issues/1889#issuecomment-2143043997
# gcc not g++

# -ldflags
#  -s    disable symbol table
#  -w    disable DWARF generation

CC=i686-linux-musl-gcc CGO_ENABLED=1 GOOS=linux GOARCH=386 \
    go build -ldflags="-s -w -linkmode external -extldflags '-static' \
    -checklinkname=0" \
    -o bin/pocketserver_ish
