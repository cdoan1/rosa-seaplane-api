# Request Limits

The Platform API enforces limits on inbound request sizes to prevent memory
exhaustion from oversized payloads.

## Request body size

All API endpoints share a global maximum request body size enforced by the
`LimitRequestBody` middleware. Enforcement has two paths depending on whether
the client declares a `Content-Length` header:

- **With `Content-Length`**: if the declared size exceeds the limit, the
  middleware returns `413 Request Entity Too Large` before any body bytes are
  read and before the handler is invoked.
- **Without `Content-Length`** (chunked or streamed): the body is wrapped with
  `http.MaxBytesReader`. When a handler reads beyond the limit it receives a
  `*http.MaxBytesError`. The middleware does not guarantee a 413 in this path —
  the handler's own error handling determines the response status code.

| Setting                  | Default    | Description                                     |
| ------------------------ | ---------- | ----------------------------------------------- |
| `MAX_REQUEST_BODY_BYTES` | `10485760` | Maximum request body in bytes (default: 10 MiB) |

The default of 10 MiB matches the AWS API Gateway maximum payload size, so
legitimate requests passing through the gateway are never rejected by this
limit. If an endpoint requires a larger body, set `MAX_REQUEST_BODY_BYTES`
at deploy time.

### Example

```bash
# Override the limit to 4 MiB
MAX_REQUEST_BODY_BYTES=4194304 ./platform-api serve
```

## Request header size

The API server caps total request header size at 16 KiB (`MaxHeaderBytes` on
the `http.Server`). This is sufficient for SigV4 `Authorization` headers
(~300 bytes) and session tokens (~400 bytes) with a large safety margin.
This value is not configurable via environment variable.
