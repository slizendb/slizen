# Security Policy

Slizen v0.1 is a developer preview and does not have a production security support window yet.

## Reporting A Vulnerability

Please report suspected vulnerabilities privately to the repository owner instead of opening a public exploit issue. Include:

- affected commit or tag;
- deployment assumptions;
- minimal reproduction;
- expected and actual impact;
- suggested mitigation, if known.

## v0.1 Security Model

- Redis or Valkey remains the source of truth.
- Slizen is not a durable database and must not be used as the authoritative store for sensitive data.
- The admin API has no built-in authentication in v0.1.
- Do not expose the admin endpoint to the public internet.
- Bind the admin API to a private interface and put external auth/network policy in front of it if needed.

## Data Handling

Slizen should not log cached values, Redis authentication data, or raw Redis keys. Prometheus labels must not contain Redis keys or unbounded user input. Hot-key admin output uses HMAC key identifiers by default.
