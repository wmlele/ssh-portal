# SSH Portal

SSH Portal is a relay-based SSH connection system designed for scenarios where both the sender and receiver are behind NAT or firewalls, making direct connections impossible. It enables secure SSH connections through an intermediary relay server, allowing temporary remote access for remote support scenarios—similar to AnyDesk or TeamViewer, but for SSH.

The system is ideal for:
- **Remote Support**: When a support technician needs to temporarily access a server or device behind a NAT/firewall
- **NAT Traversal**: When both endpoints are behind NAT and cannot establish direct connections
- **Temporary Access**: When you need to grant temporary SSH access to a network you don't have direct access to
- **On-Demand Connections**: Connection codes are time-limited and automatically expire

The system uses human-readable BIP39-based connection codes (e.g., `abandon-ability-able-about-123-4567`) for easy pairing and supports interactive terminal user interfaces (TUI) for real-time monitoring.

## Features

- **NAT/Firewall Traversal**: Enables connections when both endpoints are behind NAT or firewalls
- **Remote Support Model**: Receiver initiates connection and waits for sender to connect with a code
- **Time-Limited Access**: Connection codes expire automatically (default 10 minutes) for security
- **Relay Server**: Coordinates connections between senders and receivers without needing direct network access
- **Human-Readable Codes**: Easy-to-share BIP39-based connection codes (e.g., `abandon-ability-able-about-123-4567`)
- **Two-Part Secret Exchange**: Relay code + receiver code for enhanced security (see [KEY_EXCHANGE.md](KEY_EXCHANGE.md))
- **JSON Protocol**: All communication uses JSON after initial version handshake
- **Interactive TUI**: Real-time monitoring with status displays and log viewers
- **Automatic Invite Management**: Time-limited invites with automatic cleanup
- **Connection Tracking**: Monitor active splices (connections) and outstanding invites
- **Port Forwarding**: Supports TCP/IP port forwarding from sender to receiver
- **Session Control**: Optional session handling (PTY/shell/exec) on receiver
- **Graceful Shutdown**: Context-based shutdown for clean termination

## Architecture

The system consists of three components:

1. **Relay**: The central server that coordinates connections
   - Accepts receiver connections and creates invites via TCP JSON protocol
   - Pairs senders with receivers using connection codes
   - Bridges connections between sender and receiver
   - Displays invites and active splices in TUI
   - Shows sender and receiver addresses in tables

2. **Receiver**: The SSH server endpoint
   - Connects to relay and requests invite via TCP JSON protocol
   - Generates local receiver code and combines with relay code into user code
   - Waits for "ready" message from relay (when sender connects)
   - Handles SSH sessions (optional) and port forwarding requests
   - Displays user code, connection status, sender address, and active TCP/IP forwards in TUI

3. **Sender**: The SSH client endpoint
   - Parses user code to extract relay code and full code
   - Connects to relay using relay code (only relay code sent to relay)
   - Establishes SSH connection to receiver using full code for authentication
   - Supports dynamic port forwarding requests
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
- Use JSON protocol for all communication (after initial `ssh-relay/1.0` version line)
- Display outstanding invites and active splices in the TUI
- Show sender and receiver addresses in invite and splice tables

### Receiver

Start the receiver that accepts SSH connections:

```bash
ssh-portal receiver [flags]
```

**Flags:**
- `--relay <host>`: Relay server host (default: localhost)
- `--relay-port <port>`: Relay server TCP port (default: 4430)
- `--interactive`: Enable interactive TUI mode (default: true)
- `--session`: Enable session handling (PTY/shell/exec) (default: false)

**Example:**
```bash
# Connect to local relay
ssh-portal receiver

# Connect to remote relay
ssh-portal receiver --relay relay.example.com --relay-port 4430
```

The receiver will:
1. Generate an SSH host key fingerprint
2. Connect to relay and send mint request via TCP JSON protocol
3. Receive relay code and generate local receiver code
4. Combine codes into user code (BIP39 format) and display to user
5. Connect to relay and send hello with RID
6. Wait for "ready" message from relay (when sender connects)
7. Start SSH server and handle SSH sessions (if enabled) and port forwarding requests
8. Display user code, connection info (RID, FP), sender address, and active TCP/IP forwards in TUI

### Sender

Connect as a sender using a connection code:

```bash
ssh-portal sender --code <code> [flags]
```

**Flags:**
- `-c, --code <code>`: User code in BIP39 format (required, or set via `SSH_PORTAL_SENDER_CODE` env var)
- `--relay <host>`: Relay server host (default: localhost)
- `--relay-port <port>`: Relay server TCP port (default: 4430)
- `--interactive`: Enable interactive TUI mode (default: true)

**Example:**
```bash
# Connect using user code (BIP39 format)
ssh-portal sender --code abandon-ability-able-about-123-4567

# Using environment variable
export SSH_PORTAL_SENDER_CODE=abandon-ability-able-about-123-4567
ssh-portal sender

# Non-interactive mode
ssh-portal sender --code abandon-ability-able-about-123-4567 --interactive=false
```

The sender will:
1. Parse user code to extract relay code and full code
2. Connect to relay and send hello with relay code (only relay code sent to relay)
3. Receive receiver fingerprint from relay
4. Establish SSH connection to receiver using full code for authentication
5. Support dynamic port forwarding requests
6. Monitor connection health
7. Display connection status in TUI

## Interactive TUI

All three components support an interactive TUI mode (enabled by default). Press `q` or `Ctrl+C` to quit and gracefully shutdown.

### Relay TUI

- **Top Section**: 
  - Two-column layout showing:
    - Outstanding Invites: Code, RID, Receiver Address, Expires
    - Active Splices: Code, Sender Address, Receiver Address
- **Bottom Section**: 
  - Real-time log viewer with timestamps

### Receiver TUI

- **Top Section**: 
  - Left pane: Connection information (User Code, RID, Fingerprint, Sender Address)
  - Right pane: Active TCP/IP forwards table (Src Address, Origin, Destination)
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
   - Receiver generates local receiver code (32 bits, base64)
   - Receiver connects to relay TCP port and sends JSON mint request
   - Relay creates invite with relay code (32 bits, base64) and RID
   - Receiver combines relay code + receiver code into user code (BIP39 format)
   - Receiver sends hello with RID to relay
   - Relay attaches receiver connection to invite and waits for sender
   - Receiver displays user code and waits for "ready" message

2. **Sender Connection**:
   - User provides user code (BIP39 format) to sender
   - Sender parses user code to extract relay code and full code
   - Sender connects to relay TCP port and sends JSON hello with relay code
   - Relay validates code, finds waiting receiver
   - Relay sends "ready" message to receiver (with sender address)
   - Relay sends "ok" response to sender (with receiver fingerprint)
   - Receiver starts SSH server after receiving "ready"
   - Sender establishes SSH connection using full code for authentication

3. **Connection Splice**:
   - Relay bridges the two TCP connections (sender ↔ receiver)
   - SSH protocol flows through relay
   - Connection tracked in splice table

4. **Port Forwarding**:
   - Sender requests port forwarding via SSH `direct-tcpip` channel
   - Receiver handles `direct-tcpip` channel requests
   - Forwards data bidirectionally between sender and target destination
   - Each forward shows sender address in receiver TUI

### Protocol Details

- **Protocol**: JSON-based after initial `ssh-relay/1.0` version line
- **Invites**: Time-limited (default 10 minutes), automatically cleaned up
- **User Codes**: BIP39 format: `word-word-word-word-xxx-xxxx` (4 words + 7 digits)
- **Code Exchange**: Two-part secret (relay code + receiver code) - see [KEY_EXCHANGE.md](KEY_EXCHANGE.md)
- **RID**: Base32 rendezvous identifier for receiver connection
- **Security**: 
  - Fingerprint pinning ensures sender connects to correct receiver
  - Two-part secret: relay never sees receiver code
  - Full code required for SSH authentication (relay code alone insufficient)

## Examples

### Basic Usage

**Terminal 1 - Start Relay:**
```bash
ssh-portal relay
```

**Terminal 2 - Start Receiver:**
```bash
ssh-portal receiver
# Note the user code displayed in TUI (e.g., "abandon-ability-able-about-123-4567")
```

**Terminal 3 - Connect Sender:**
```bash
ssh-portal sender --code abandon-ability-able-about-123-4567
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
ssh-portal sender --code abandon-ability-able-about-123-4567 --relay relay.example.com --relay-port 4430
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
- **Two-Part Secret Exchange**: Relay code + receiver code provides additional security (relay never sees receiver code)
- **Time-Limited Invites**: Invites expire after 10 minutes (configurable)
- **One-Time Use**: Invites are deleted after successful pairing
- **SSH Protocol**: Uses standard SSH protocol with host key verification
- **Full Code Authentication**: Requires both relay code and receiver code for SSH authentication

For detailed information on the key exchange protocol, see [KEY_EXCHANGE.md](KEY_EXCHANGE.md).

## License

[Add your license information here]

## Contributing

[Add contribution guidelines here]

