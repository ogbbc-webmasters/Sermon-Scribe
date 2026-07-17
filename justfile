# Compile the Elm frontend to web/elm.js.
build-elm:
    elm make src/Main.elm --optimize --output=web/elm.js

# Build the server binary (embeds web/, so the Elm build runs first).
build: build-elm
    go build -o sermon-scribe ./cmd/sermon-scribe

# Run the server locally.
run: build
    ./sermon-scribe

# Run the test suite (go:embed of web/ needs elm.js to exist).
test: build-elm
    go test ./...

# Build and deploy as a systemd service on this VM (data lives in
# /home/exedev/sermon-scribe-data; binary installed to /usr/local/bin).
deploy: build
    mkdir -p /home/exedev/sermon-scribe-data
    sudo install -T sermon-scribe /usr/local/bin/sermon-scribe
    sudo cp deploy/sermon-scribe.service /etc/systemd/system/sermon-scribe.service
    sudo systemctl daemon-reload
    sudo systemctl enable sermon-scribe
    sudo systemctl restart sermon-scribe
