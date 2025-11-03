# SSH Portal

SSH Portal is a relay-based SSH connection system that enables secure SSH connections between a sender and receiver through an intermediary relay server. The system uses human-readable connection codes (e.g., `alpha-bravo-1234`) for easy pairing and supports interactive terminal user interfaces (TUI) for real-time monitoring.

## Features

- **Relay Server**: Coordinates connections between senders and receivers
- **Human-Readable Codes**: Easy-to-share connection codes (e.g., `alpha-bravo-1234`)
- **Interactive TUI**: Real-time monitoring with status displays and log viewers
- **Automatic Invite Management**: Time-limited invites with automatic cleanup
- **Connection Tracking**: Monitor active splices (connections) and outstanding invites
- **Port Forwarding**: Supports TCP/IP port forwarding from sender to receiver
- **Graceful Shutdown**: Context-based shutdown for clean termination

## Architecture

The system consists of three components:

1. **Relay**: The central server that coordinates connections
   - Accepts receiver connections and creates invites
   - Pairs senders with receivers using connection codes
   - Bridges connections between sender and receiver
   - Provides HTTP API for invite minting
   - Displays invites and active splices in TUI

2. **Receiver**: The SSH server endpoint
   - Connects to relay and receives an invite code
   - Waits for sender to connect
   - Handles SSH sessions and port forwarding requests
   - Displays connection status and active TCP/IP forwards in TUI

3. **Sender**: The SSH client endpoint
   - Connects to relay using a connection code
   - Establishes SSH connection to receiver
   - Creates local port forwarding (default: `127.0.0.1:10022` → `127.0.0.1:22`)
   - Displays connection status in TUI

## Installation

### Build from Source

```bash
# Clone the repository
git clone <repository-url>
cd ssh-portal

# Build using Task
task build

# Or build directly with go
go build -o bin/ssh-portal ./cmd/ssh-portal
```

The binary will be created at `bin/ssh-portal`.

## Usage

### Relay Server

Start the relay server that coordinates connections:

```bash
ssh-portal relay [flags]
```

**Flags:**
- `--port <port>`: TCP port for relay (default: 4430)
- `--interactive`: Enable interactive TUI mode (default: true)
- `--config <file>`: Config file path (optional)
- `--log-level <level>`: Log level: debug|info|warn|error (default: info)

**Example:**
```bash
# Start relay on default port 4430
ssh-portal relay

# Start relay on custom port
ssh-portal relay --port 5000

# Non-interactive mode (logs to stdout)
ssh-portal relay --interactive=false
```

The relay server will:
- Listen on the specified TCP port for sender/receiver connections
- Listen on `TCP_PORT + 1` for HTTP API (e.g., 4431 if TCP port is 4430)
- Display outstanding invites and active splices in the TUI

**HTTP API:**
- `POST /mint`: Create a new invite
  ```json
  {
    "receiver_fp": "SHA256:...",
    "ttl_seconds": 600
  }
  ```
  Response:
  ```json
  {
    "code": "alpha-bravo-1234",
    "rid": "base32_rendezvous_id",
    "exp": "2024-01-01T12:00:00Z"
  }
  ```

### Receiver

Start the receiver that accepts SSH connections:

```bash
ssh-portal receiver [flags]
```

**Flags:**
- `--relay <host>`: Relay server host (default: localhost)
- `--relay-port <port>`: Relay server TCP port (default: 4430)
- `--interactive`: Enable interactive TUI mode (default: true)
- `--log-level <level>`: Log level (default: info)

**Example:**
```bash
# Connect to local relay
ssh-portal receiver

# Connect to remote relay
ssh-portal receiver --relay relay.example.com --relay-port 4430
```

The receiver will:
1. Generate an SSH host key fingerprint
2. Mint an invite from the relay via HTTP API
3. Connect to the relay and send HELLO with the RID
4. Wait for sender to connect using the code
5. Handle SSH sessions and port forwarding requests
6. Display connection info (Code, RID, FP) and active TCP/IP forwards in TUI

### Sender

Connect as a sender using a connection code:

```bash
ssh-portal sender --code <code> [flags]
```

**Flags:**
- `-c, --code <code>`: Connection code (required, or set via `SSH_PORTAL_SENDER_CODE` env var)
- `--relay <host>`: Relay server host (default: localhost)
- `--relay-port <port>`: Relay server TCP port (default: 4430)
- `--interactive`: Enable interactive TUI mode (default: true)
- `--log-level <level>`: Log level (default: info)

**Example:**
```bash
# Connect using code
ssh-portal sender --code alpha-bravo-1234

# Using environment variable
export SSH_PORTAL_SENDER_CODE=alpha-bravo-1234
ssh-portal sender

# Non-interactive mode
ssh-portal sender --code alpha-bravo-1234 --interactive=false
```

The sender will:
1. Connect to relay and perform handshake with code
2. Establish SSH connection to receiver
3. Create local port forward on `127.0.0.1:10022` → `127.0.0.1:22`
4. Monitor connection health
5. Display connection status in TUI

## Interactive TUI

All three components support an interactive TUI mode (enabled by default). Press `q` or `Ctrl+C` to quit and gracefully shutdown.

### Relay TUI

- **Top Section**: 
  - Two-column layout showing:
    - Outstanding Invites: Code, RID, Expires
    - Active Splices: Code, Bytes Up/Down, Sender Address
- **Bottom Section**: 
  - Real-time log viewer with timestamps

### Receiver TUI

- **Top Section**: 
  - Connection information: Code, RID, Fingerprint
  - Active TCP/IP forwards table: Origin → Destination
- **Bottom Section**: 
  - Real-time log viewer with timestamps

### Sender TUI

- **Top Section**: 
  - Connection status: Connecting / Connected / Failed
  - Status messages with error details on failure
- **Bottom Section**: 
  - Real-time log viewer with timestamps

## Configuration

SSH Portal supports configuration via:

1. **Command-line flags** (highest priority)
2. **Environment variables**
3. **Config file** (`./configs/config.yaml` by default)
4. **Defaults**

**Example config file:**
```yaml
log:
  level: info

sender:
  code: "alpha-bravo-1234"  # Optional default code
```

**Environment Variables:**
- `SSH_PORTAL_SENDER_CODE`: Default connection code for sender
- `SSH_PORTAL_LOG_LEVEL`: Default log level

## How It Works

### Connection Flow

1. **Receiver Setup**:
   - Receiver generates SSH host key and fingerprint
   - Receiver mints an invite via HTTP `POST /mint` with fingerprint
   - Relay creates invite with code (e.g., `alpha-bravo-1234`) and RID
   - Receiver connects to relay TCP port and sends `HELLO receiver rid=<rid>`
   - Relay attaches receiver connection to invite and waits for sender

2. **Sender Connection**:
   - Sender connects to relay TCP port and sends `HELLO sender code=<code>`
   - Relay validates code, checks receiver is ready
   - Relay sends greeting with receiver fingerprint
   - Sender validates fingerprint and establishes SSH connection

3. **Connection Splice**:
   - Relay bridges the two TCP connections (sender ↔ receiver)
   - SSH protocol flows through relay
   - Connection statistics tracked (bytes up/down)

4. **Port Forwarding**:
   - Sender requests port forwarding (e.g., local `127.0.0.1:10022` → remote `127.0.0.1:22`)
   - Receiver handles `direct-tcpip` channel requests
   - Forwards data bidirectionally between sender and target destination

### Protocol Details

- **Invites**: Time-limited (default 10 minutes), automatically cleaned up
- **Codes**: Human-readable format: `<word>-<word>-<4-digit-number>`
- **RID**: Base32 rendezvous identifier for receiver connection
- **Security**: Fingerprint pinning ensures sender connects to correct receiver

## Examples

### Basic Usage

**Terminal 1 - Start Relay:**
```bash
ssh-portal relay
```

**Terminal 2 - Start Receiver:**
```bash
ssh-portal receiver
# Note the connection code displayed in TUI (e.g., "alpha-bravo-1234")
```

**Terminal 3 - Connect Sender:**
```bash
ssh-portal sender --code alpha-bravo-1234
```

**Terminal 4 - Use SSH:**
```bash
# Now you can SSH through the forwarded port
ssh -p 10022 user@127.0.0.1
```

### Remote Relay Setup

**On Relay Server:**
```bash
ssh-portal relay --port 4430
```

**On Receiver Machine:**
```bash
ssh-portal receiver --relay relay.example.com --relay-port 4430
```

**On Sender Machine:**
```bash
ssh-portal sender --code alpha-bravo-1234 --relay relay.example.com --relay-port 4430
```

## Logging

Logs include timestamps and are captured in the TUI when interactive mode is enabled. In non-interactive mode, logs are written to stdout/stderr.

**Log Levels:**
- `debug`: Verbose debugging information
- `info`: General informational messages (default)
- `warn`: Warning messages
- `error`: Error messages only

## Development

### Building

```bash
# Build binary
task build

# Run tests
task test

# Lint code
task lint

# Clean build artifacts
task clean
```

### Project Structure

```
ssh-portal/
├── cmd/
│   └── ssh-portal/
│       └── main.go          # Entry point
├── internal/
│   └── cli/
│       ├── relay/          # Relay server implementation
│       ├── receiver/       # Receiver implementation
│       ├── sender/         # Sender implementation
│       └── tui/            # Shared TUI components
├── configs/
│   └── config.yaml         # Example config
└── go.mod                   # Go module
```

## Security Considerations

- **Fingerprint Pinning**: Senders validate receiver fingerprints to prevent MITM attacks
- **Time-Limited Invites**: Invites expire after 10 minutes (configurable)
- **One-Time Use**: Invites are deleted after successful pairing
- **SSH Protocol**: Uses standard SSH protocol with host key verification

## License

[Add your license information here]

## Contributing

[Add contribution guidelines here]

