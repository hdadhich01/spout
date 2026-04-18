# Tiers

Tiers describe the **encryption level** of a stream, not different products
or servers. Same CLI, same server, same dashboard - the difference is
whether the server sees plaintext or ciphertext.

```
┌─────────────────────────────────────────────────────────┐
│                                                          │
│   Tier 1a: nc  ──plaintext TCP──→ Server                 │
│                                     │                    │
│   Tier 1b: CLI ──plaintext WS ──→  Server  ─WS→ Browser  │
│                                     │     (no decrypt)   │
│   Tier 2:  CLI ──ciphertext WS──→  Server  ─WS→ Browser  │
│                                     │      (WebCrypto    │
│                                     │       decrypts)    │
│                                                          │
│                              Same server.                │
│                              Same dashboard.             │
│                              Same storage.               │
└─────────────────────────────────────────────────────────┘
```

| Tier | Transport | Server sees | nc? | CLI needed? | Browser decrypts? |
|---|---|---|---|---|---|
| 1a | TCP | Plaintext | Yes | No | No |
| 1b | WebSocket | Plaintext | - | Yes | No |
| 2 | WebSocket | Ciphertext | - | Yes | Yes (WebCrypto) |

## Tier 2 encryption `[planned]`

```
CLI:
  key = random 256-bit AES-GCM key
  for each chunk:
    nonce = random 96-bit
    ciphertext = AES-GCM-Encrypt(key, nonce, chunk)
    send(nonce + ciphertext)
  print URL: spout.sh/r/wolf-a3f2#key=base64(key)

Server:
  receives bytes
  stores bytes           (doesn't know or care if encrypted)
  relays bytes to viewers

Browser:
  reads #key= from URL fragment      (never sent to server)
  imports via crypto.subtle.importKey()
  for each message:
    split nonce (first 12 bytes) and ciphertext
    plaintext = crypto.subtle.decrypt(key, nonce, ciphertext)
    term.write(plaintext)
```

Key lives in the URL fragment because fragments are never transmitted to the
server. Sharing the URL shares the key - anyone with the link can view.

## Which tier do I get?

| Default for | Tier |
|---|---|
| `command \| spout` to `spout.sh` | 2 (E2E on by default) |
| `command \| spout --local` | 1b |
| `command \| spout -s self-hosted` | 1b (self-hosted is trusted) |
| `command \| spout --no-encrypt` | 1b (force plaintext) |
| `cmd \| nc spout.sh 1337` | 1a (plaintext, no CLI needed) |

All four produce the same-shape session on the server. The tier only changes
whether the stored bytes are ciphertext or plaintext.
