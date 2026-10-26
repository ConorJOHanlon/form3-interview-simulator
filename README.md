# 💸 Payment Scheme Simulator

A TCP simulator for the payment scheme in the [exercise brief](https://github.com/form3tech-oss/interview-simulator). It accepts payments, simulates how long the counterparty takes to process them, and shuts down gracefully without leaving anyone hanging.

Curious why something works the way it does? Every assumption and trade-off is in [DECISIONS.md](DECISIONS.md) 🧭

## 🚀 Getting started

### Run it

You'll need **Go 1.26** or later.

```sh
go run ./cmd/simulator
```

That's it. The simulator is now listening on `:8080`. Add `-h` for a quick summary of how to use it.

### Send your first payment

Open another terminal and send a few payments:

```console
$ nc localhost 8080
PAYMENT|10
RESPONSE|ACCEPTED|Transaction processed
PAYMENT|500
RESPONSE|ACCEPTED|Transaction processed      ⏱️ after 500ms
PAYMENT|-5
RESPONSE|REJECTED|Invalid amount
```

Amounts up to 100 are processed instantly. Above that, the simulator waits one millisecond for every unit of the amount, so 500 takes 500ms, up to a maximum of 10 seconds.

### Debug your client

The simulator logs every request it handles, with the response it generated and how long that took. The line is written just before the response is sent, so it doesn't confirm the client received it:

```
level=INFO msg="request handled" remote_addr=127.0.0.1:62629 request=PAYMENT|500 response="RESPONSE|ACCEPTED|Transaction processed" duration=500.611917ms
```



### Build a binary

```sh
go build -o simulator ./cmd/simulator
./simulator
```



## 🛑 Graceful shutdown



### What happens

Send `PAYMENT|9000`, then stop the simulator with **Ctrl+C** or `kill -TERM <pid>`:

1. 🚪 **New connections are turned away** straight away.
2. 🤝 **Existing connections keep being served**, so clients can still send requests.
3. ⏳ **After a 3-second grace period**, any payment still processing gets `RESPONSE|REJECTED|Cancelled`, idle connections are closed, and the process exits.



### Skip the wait

In a hurry? ⚡ Press **Ctrl+C** again to exit immediately.

### Change the settings

The address and grace period live in `[cmd/simulator/main.go](cmd/simulator/main.go)`.

## 🧪 Testing



### Run the tests

```sh
go test -race ./...         # the full suite, about 12s
go test -race -short ./...  # skip the 10-second delay test ⚡
go test -v ./...            # list every behaviour under test
go test -fuzz=FuzzParsePayment -fuzztime=30s ./internal/scheme  # 🎲 throw random input at the parser
```



### Test the real process

A separate suite runs the simulator as its own process, to check what the other tests can't: command-line handling, exit codes, and real `SIGINT`/`SIGTERM` signals. It needs macOS or Linux.

```sh
go test -race -tags=integration ./cmd/simulator  # 🖥️ adds the process tests
```

### Test layers


| Package              | What it covers                                                                                                               |
| -------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| 🧩 `internal/scheme` | Parsing, validation, delays and cancellation. No network needed.                                                             |
| 🔌 `internal/server` | Connections, request framing and graceful shutdown, using stub handlers so every test controls exactly when requests finish. |
| 🌍 `cmd/simulator`   | End to end: the real scheme over real TCP, shutdown included.                                                                |
| 🖥️ `cmd/simulator` with `-tags=integration` | The real process: command-line handling, exit codes and signals. |




## 🗺️ Project layout

```
cmd/simulator/               🚦 entry point: config, signals, exit codes
internal/scheme/             💳 the payment protocol: parsing, validation, processing
internal/server/             🔌 a protocol-agnostic TCP server with graceful shutdown
internal/server/servertest/  🧰 test helpers: a server on a random port and a test client
```
