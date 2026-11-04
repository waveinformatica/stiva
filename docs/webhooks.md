# Webhooks

Stiva can notify other systems when something happens in a registry. Webhooks
are managed under **Administration → Webhooks** (or `/api/v1/admin/webhooks`).

## Events

| Event | When |
|---|---|
| `push.manifest` | an image manifest was pushed (data: `repo`, `reference`, `digest`, `media_type`, `size`) |
| `delete.manifest` | an image manifest was deleted (`repo`, `reference`) |
| `push.artifact` | a file was uploaded to a non-OCI registry (`path`, `format`, `content_type`) |
| `delete.artifact` | a file was deleted (`path`) |
| `job.finished` | a background job finished (`id`, `kind`, `status`, `dry_run`, `trigger`, `error`) |
| `security.signature_denied` | a pull was refused by the registry's cosign policy (`repo`, `reference`, `digest`, `signature`) |
| `security.checksum_mismatch` | an upstream served content that failed its digest or checksum (`error`, `path`) |
| `ping` | the "Send test" button |

A webhook subscribes to names or patterns: `push.manifest`, `push.*`, `*`. It can
be limited to some registries; with none selected it receives every registry's
events.

## Delivery

Each event is a `POST` with a JSON body:

```json
{
  "id": "6f1c…",
  "event": "push.manifest",
  "time": "2026-11-04T10:42:18Z",
  "registry": "docker",
  "actor": "ivano",
  "data": { "repo": "acme/app", "reference": "v1.2.0", "digest": "sha256:…" }
}
```

Headers: `X-Stiva-Event`, `X-Stiva-Delivery` (a number, stable across retries, use
it to de-duplicate) and, when the webhook has a secret, `X-Stiva-Signature`.

Any `2xx` answer counts as delivered. Anything else (or no answer within 10
seconds) is retried after 30 s, 2 min, 10 min, 1 h and 6 h, six attempts in all;
then the delivery is marked failed and can be redelivered by hand. Redirects are
**not** followed. Events are queued in PostgreSQL before they are sent, so a
restart does not lose them; delivery history is kept for 14 days.

## Verifying the signature

`X-Stiva-Signature` is `sha256=` followed by the hex HMAC-SHA256 of the raw
request body, keyed with the webhook's secret (a stored credential — it is never
shown again). Compare in constant time:

```python
import hmac, hashlib

def valid(secret: bytes, body: bytes, header: str) -> bool:
    expected = "sha256=" + hmac.new(secret, body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, header)
```

Webhook URLs are set by administrators and may point at internal hosts; there is
no filter on private addresses, so grant the admin role accordingly.
