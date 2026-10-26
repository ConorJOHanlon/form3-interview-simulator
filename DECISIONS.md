# 🧭 Decisions

Where the brief left room for interpretation, here's what I decided and why.

## 🧑‍💻 Built for the engineers who use it

- **Product assumption.** I assumed the primary user is an engineer validating a payment client before integrating with a real scheme. They need to reproduce immediate responses, processing delays, validation failures and shutdown cancellation predictably. I prioritised fidelity to the specified protocol and explicit request outcomes, because any difference between the simulator and the scheme could give client tests false confidence. The README examples and automated tests make each behaviour easy to exercise and verify.
- **Quality-of-life improvements.** Beyond the brief, I made a few small improvements to help engineers use the simulator effectively. Each is a few lines, and none adds configuration or changes the scheme's behaviour.
- **See what happened.** Every request is logged with the response generated for it and its handling time, because seeing what the simulator received and how it answered is the first step in debugging a client. The line is written just before the response is sent, so it doesn't confirm delivery. The startup log shows the grace period, so it's clear how long a shutdown can take. Logs go to stderr via `slog`. This is noisy under load tests; a log level setting would fix that, but I kept configuration to a struct to stay focused.
- **Fast restarts.** A second `SIGINT`/`SIGTERM` exits immediately, because waiting out the grace period on every local restart is tedious.
- **No surprises from the command line.** `-h` explains what the simulator does and how to use it. Unknown flags or arguments fail with the usage text instead of silently starting a server on `:8080`.
- **Running in 30 seconds.** The README has a copy-paste `nc` example, shows what the request log looks like, and spells out the shutdown behaviour.
- **Deliberately not added.** Flags or environment variables for the address and grace period, and scenario triggers (e.g. a magic amount that forces a rejection or a dropped connection). The first was omitted to keep the solution focused, since the brief gives it no extra credit, and the second would change the scheme's specified behaviour.
- **What comes next depends on users.** Before extending the simulator, I'd ask its users which integration failures they struggle to reproduce. If missing failure scenarios were the main limitation, I'd prioritise explicit, repeatable scenario controls while preserving the default scheme behaviour. I'd judge their value by whether engineers could reproduce and diagnose those failures more quickly.



## 🏗️ Architecture

- **Transport and scheme are separate.** `internal/server` is a protocol-agnostic TCP server: connections, line framing and graceful shutdown. `internal/scheme` implements the payment protocol. They meet at a single `Handler` function type, like `http.HandlerFunc`, so a new scheme is a new handler with no server changes.
- **The boundary pays for itself in tests.** Server tests use stub handlers, which makes shutdown tests deterministic instead of sleep-based. `servertest` provides `httptest`-style helpers shared by the server and end-to-end tests.
- **Extension points left for later.** Configurable delay rules, dispatch on message type, and letting handlers close connections were deliberately not built. Each is a change within a single package once it's needed.



## 💳 Protocol

- **Request format.** Requests must be exactly `PAYMENT|<amount>` (case-sensitive). A trailing `\r` is tolerated for `telnet`-style clients.
- **Amount validation.** The amount must be digits only and at least 1. Signs, zero, whitespace, decimals and overflow are `Invalid amount`. Leading zeros are allowed.
- **Amounts are in minor units.** The brief names no currency or unit. I treat the amount as a whole number of minor units (pence or cents), the usual way payment systems avoid rounding errors, which is also why decimals are rejected. The simulator only uses the amount to set the processing delay, so the unit never changes its behaviour.
- **No maximum amount.** The brief sets no limit, so anything up to the largest integer (9,223,372,036,854,775,807 on 64-bit platforms) is accepted, and larger amounts are `Invalid amount`. Real schemes do have limits, but adding one would reject payments the brief says to accept.
- **Framing.** A request is only processed once its `\n` arrives. Unterminated data is discarded when the client closes. Lines over 1 KiB (including the `\n`) close the connection without a response, as a request that "can't be safely handled".
- **Ordering.** Requests on one connection are answered strictly in order, even when sent together: an instant payment never overtakes a delayed one ahead of it.



## 🛑 Graceful shutdown

- **Accepted for processing** means the request's full line has been read. Anything less can be discarded without a response. This is distinct from `RESPONSE|ACCEPTED`: receiving a request, processing it and delivering its response are separate events. A request accepted for processing may still be `Cancelled`, and a response can be written without the client receiving it. A client that retries after a lost response sends what looks like a new payment, because the protocol carries no payment ID, so payments are not deduplicated.
- **Sequence.** On `SIGTERM`/`SIGINT` the listener closes. Existing connections keep being served for the grace period (default 3s), as the brief allows, and shutdown ends early if every client disconnects. When the grace period expires, in-flight requests get `Cancelled`, and idle or half-sent connections are closed without a response.
- **Why two contexts.** Stopping new connections and cancelling requests happen at different times, so one context can't express both. The shutdown signal closes the listener immediately. Requests get their own context, detached from that signal with `context.WithoutCancel` and cancelled only when the grace period expires. Pending reads are interrupted with a read deadline rather than by closing the connection, so a `Cancelled` response can still be written.
- **Idle connections hold shutdown open.** An idle keep-alive client makes shutdown take the full grace period. Closing idle connections immediately would drain faster, but it would stop serving requests the brief says the server may still accept.
- **Requests after the deadline.** A payment read after the grace period expires (for example, one already buffered) is rejected as `Cancelled`, even with no delay. Invalid requests are still answered as usual. A request that completes exactly at the deadline may get either response.
- **Exiting.** Exit 0 after shutdown. Exit non-zero if the server can't start or accepting connections fails unexpectedly, so the platform can restart it; in that case in-flight requests are cancelled at once.
- **Clients that disconnect mid-payment.** The payment still runs to completion. Noticing the disconnect would need a second reader on every connection, which conflicts with reading requests one at a time. The cost is bounded (at most 10s per connection), but such a connection can hold shutdown open until the grace period expires.
- **Connections mid-handshake at shutdown.** A connection the operating system has completed but the server hasn't yet picked up is reset when the listener closes, rather than refused. None of its requests were accepted for processing, so this is consistent with the rules above.
- **No write timeouts.** Slow clients are out of scope, and `SIGKILL` is the backstop.
- **`SIGHUP` is not handled.** Closing the terminal kills the simulator immediately, without a grace period. Nobody is left to read the responses, and many servers use `SIGHUP` to reload, so treating it as a shutdown would surprise people.
- **Windows.** Ctrl+C and Ctrl+Break work as on macOS and Linux: a graceful shutdown, then an immediate exit on the second press. The process tests are Unix-only, because Windows can't send these signals to another process.



## ☁️ Deployment

- **Stateless.** Nothing is stored and connections share no state, so instances can be scaled or replaced freely.
- **Configuration.** A struct with defaults (listen address `:8080`, grace period 3s), as the brief allows. In production the grace period would come from the environment, because it has to be tuned to each platform's kill timeout. The default address binds all interfaces, since `localhost` is unreachable from outside a container.
- **Platform timeouts.** The grace period must be shorter than the platform's kill timeout (30s by default in Kubernetes), otherwise in-flight requests are killed without a response.
- **Load balancers.** Connections routed here after `SIGTERM` are refused. Draining (e.g. a `preStop` hook) is a deployment concern.



## 🧪 Testing and scope

- **Process integration tests.** An opt-in `integration` build tag adds a fourth layer: separate OS processes running the real entry point, driven by TCP clients and actual signals. The test helper changes only the child process configuration (a random loopback port and scenario-specific grace period), so instances can run concurrently without production flags or fixed-port conflicts. Go verbose output provides the scenario report, and `-json` supports automation. This verifies application startup and signal wiring; packaging is outside its scope.
- **Test layers.** Scheme unit tests (no network), server tests with stub handlers, and end-to-end tests of the real scheme over TCP. Each test starts its own server on `127.0.0.1:0`. Timing checks use real time with roughly 50ms tolerance, as the brief allows. Tests run with `-race`, and `-short` skips the 10-second delay test.
- **No queue or worker pool.** Each connection has its own goroutine, and processing is a timer, not CPU work. Queueing would push responses past the delay the scheme specifies. A CPU-bound or rate-limited scheme would instead cap the requests in progress and reject the excess with an explicit response.
- **Out of scope**, per the brief: metrics, tracing, health checks, rate limiting, release artifacts, and half-open connections or network partitions.

