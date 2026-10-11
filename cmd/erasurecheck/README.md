# Erasure acknowledgment checker

`erasurecheck` validates a supplied status snapshot against a paired tombstone
and an independently supplied expectation (T-M4-07 E12b, ADR-0035).
An explicit provider mode acquires the pair through verified mesh reads.

```sh
erasurecheck -erasure-id E -user-id U -tenant-id '' \
  -required admin-ui,media-ui,request-media,userdata-local \
  -tombstone-file tombstone.json < status.json
```

`status.json` must be one `GetUserErasureStatusResponse` proto-JSON object;
`tombstone.json` must be one `UserErasure` object, not an exported ledger or a
list response. Each input is limited to 1 MiB. The expected erasure, immutable
user and tenant IDs must match the paired inputs. The status has no user ID:
the mandatory tombstone supplies that association. An omitted tenant flag means
the empty household tenant. User and tenant IDs follow the ledger SDK's field
rules: valid UTF-8, at most 256 bytes, no surrounding whitespace or control
characters; only the tenant may be empty.

The required CSV must come from the caller's independently selected configuration
(the household generator is `scripts/gen-erasure-set.sh`). Whitespace around
CSV entries is trimmed; case is preserved. Empty, duplicate or invalid module
IDs are refused. Status rows marked required must match this set exactly,
each must have `ERASURE_OUTCOME_OK` and an acknowledgment timestamp, and
`complete` must be true. Unique optional rows with `OK`, `FAILED` or
`UNSUPPORTED` acknowledgments are allowed and do not count toward this set.

Proto-JSON camelCase and original snake_case field names are accepted, as are
named or defined integer enums. Unknown fields, duplicate keys or aliases,
nulls, wrong types, fractional enums and trailing documents are refused.
All deletion and acknowledgment times must use UTC RFC3339 with `Z` and at
most nine fractional digits. Deletion times must represent the same instant;
there is no wall-clock, acknowledgment ordering or retention policy.

Success prints only `OK snapshot acknowledgment check required=N`. Failures
print a fixed diagnostic code, never IDs, paths or raw input. Exit codes are
0 for a valid snapshot (or help), 1 for input/verification/output failure, and
2 for invalid command usage or expectations.
Flags follow Go's command parser: repeated flags use the last supplied value.

The default snapshot mode performs no network requests. Its success does not
establish snapshot provenance or current provider state. `Check` remains the
same pure validator in both modes.

## Read from the provider

```sh
erasurecheck -from-provider -admin-bearer-file /run/private/admin-session \
  -erasure-id E -user-id U -tenant-id '' \
  -required admin-ui,media-ui,request-media,userdata-local
```

This mode requires an **existing enrolled, allowlisted consumer identity** and
an **existing admin session** for the target tenant. `mvp-smoke` is not an
allowlisted ledger reader. The checker is not packaged into consumer images;
running it in the intended existing identity context is a separate prerequisite.
It neither enrolls an identity nor changes the provider's consumer allowlist.

Configure the existing identity with `MUXCORE_MODULE_ID`, `MUXCORE_GRPC_ADDR`
(the core discovery address), and `MUXCORE_TLS_CERT`, `MUXCORE_TLS_KEY`,
`MUXCORE_TLS_CA`. If both certificate and key paths are absent, an explicitly
configured `MUXCORE_TLS_DIR` supplies `module.crt` and `module.key`. Independently,
an absent CA path uses that directory's `ca.crt`. Partial explicit pairs and
explicit-file load failures are refused; they never fall back. There is no
default identity directory, system-root fallback or enrollment. All three
plaintext switches (`MUXCORE_INSECURE_DISABLE_TLS`, `MUXCORE_DEV_TLS_SKIP`,
`MUXCORE_GRPC_INSECURE`) are refused even in the dev profile.

Core must prove the mesh identity `muxcored`; discovery selects the exclusive
`identity` provider, which must prove its discovered module identity. Both hops
verify the configured mesh CA, server SAN and exact certificate CN. Caller
certificate CN must match `MUXCORE_MODULE_ID`. There is no direct provider
address or certificate-name override; `MUXCORE_TLS_SERVER_NAME` cannot override
these checks.

The bearer file contains a raw existing session token, optionally followed by
a newline. It must be a regular file, at most 8 KiB; symlinks and other special
files are refused. The opened file is rechecked and reads detect excess growth.
Empty values, nonprintable/non-ASCII values, embedded whitespace and `Bearer `
prefixes are refused. The provider validates the opaque session; the checker
does not infer credential type from a prefix. Tokens are never accepted as literal
flags or environment values. No login or API-key exchange is performed; an
unexchanged API key is not an admin session in auth-local.
`-admin-bearer-file` requires `-from-provider`, which excludes `-tombstone-file`;
provider mode does not consume stdin.

Discovery and `ListUserErasures` receive no bearer. Targeted
`GetUserErasureStatus` receives exactly one `x-auth-token` on the same verified
provider connection. The provider independently checks consumer admission for
List and the session's current admin authority and tenant for Status.

One 30-second budget covers all RPCs, not local file I/O. Responses have a
1 MiB gRPC receive limit. The ledger search requests 100 rows per page and reads
at most 100 pages; larger returned pages, continuation tokens above 4,096 bytes,
cycles, empty continuing pages and malformed or duplicate observed rows fail.
After finding the exact erasure ID, the checker validates the entire matching
page and continuation before requesting status. It does not read later pages;
this relies on the provider contract's unique, immutable erasure IDs. No target
within the bounded search means failure, even if a larger ledger might contain
it later. User/tenant binding applies to the target; normal unrelated rows are
allowed. There is no polling or completion wait.

Provider success prints only `OK provider acknowledgment check required=N`;
acquisition failures print a fixed code. IDs, tokens, paths and provider errors
are never echoed. CLI usage and expectation failures keep the existing snapshot
diagnostic format. Reads are sequential observations, not an atomic snapshot.
Neither mode acknowledges, deletes, restores or writes credentials. Passing
checks establish no owner's actual data disposition, purge, live deployment
acceptance or complete household E12 acceptance.
