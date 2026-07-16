# Build the server binary (will also compile the Elm frontend once it exists).
build:
    go build -o sermon-scribe .

# Run the server locally.
run: build
    ./sermon-scribe

# Run the test suite.
test:
    go test ./...
