# Compile the Elm frontend to web/elm.js.
elm:
    elm make src/Main.elm --optimize --output=web/elm.js

# Build the server binary (embeds web/, so the Elm build runs first).
build: elm
    go build -o sermon-scribe .

# Run the server locally.
run: build
    ./sermon-scribe

# Run the test suite (go:embed of web/ needs elm.js to exist).
test: elm
    go test ./...
