# Profile fixtures

Payloads for the two Sentry profiling wire formats, used by the profile parser
tests. Every file is authored here, not captured from a live SDK and not copied
from another project: `getsentry/relay` ships equivalent fixtures but is
FSL-1.1 licensed, which is not a license we can vendor from.

The structures below were derived by reading the SDK and Relay sources:

| Source | What it settles |
|---|---|
| `getsentry/relay` `relay-profiling/src/sample/{v1,v2}.rs`, `sample/mod.rs` | Field names, aliases, required vs optional, validation rules |
| `getsentry/sentry-php` `src/Profiling/Profile.php` | What Laravel and other PHP apps actually emit |
| `getsentry/sentry-python` `sentry_sdk/profiler/{transaction_profiler,continuous_profiler,utils}.py` | What `sentry_sdk` emits in both modes |
| `getsentry/sentry-python` `sentry_sdk/tracing.py` | How a transaction points at a v2 chunk |

## Files

| File | Format | Covers |
|---|---|---|
| `v1_php_laravel.json` | v1 `profile` | `transaction` singular, `elapsed_since_start_ns` as a JSON **number**, no `in_app`, no `thread_metadata`, thread id always `"0"` |
| `v1_python.json` | v1 `profile` | `transactions` **plural**, every numeric field as a **string**, `in_app` present, real OS thread ids |
| `v1_cocoa.json` | v1 `profile` | Unsymbolicated `instruction_addr` frames, `debug_meta`, `queue_metadata`, `relative_start_ns` / `relative_end_ns` |
| `v2_python_chunk.json` | v2 `profile_chunk` | **No top-level `timestamp`**, no os/device/runtime, no `debug_meta` |
| `v2_cocoa_chunk.json` | v2 `profile_chunk` | Top-level `timestamp` present, hex-address thread ids |
| `v2_transaction_link.json` | `transaction` | The event that links a transaction to a v2 chunk |

## Rules the parser has to honour

Each of these is a real divergence between SDKs, not a hypothetical.

1. **Numbers arrive as numbers or as strings.** Relay deserializes `thread_id`,
   `elapsed_since_start_ns`, `active_thread_id`, `relative_start_ns` and
   `relative_end_ns` with `deserialize_number_from_string`. PHP sends
   `elapsed_since_start_ns` as an int, Python sends it as a string. Both are valid.

2. **`transaction` and `transactions` both occur.** PHP sends the singular
   object, Python sends a one-element array. Relay prefers the singular and
   falls back to the first array element.

3. **Frame keys have aliases**: `name` for `function`, `file` for `filename`,
   `line` for `lineno`, `column` for `colno`. `event_id` is aliased as `profile_id`.

4. **Stacks are leaf-first.** Python walks `frame.f_back` from the current frame
   outward; Excimer reports innermost-first. The root of the flame graph is the
   **last** entry in a stack.

5. **Thread ids are strings in the normalized form.** v1 carries integers
   (or stringified integers), v2 carries opaque strings such as hex addresses.

6. **v2 chunks may have no top-level timestamp.** The Python SDK omits it, so
   chunk start and end must be derived from the samples themselves.

7. **`in_app` is not always present.** Python sets it, PHP does not. Absent is
   distinct from false.

## Limits worth encoding

From `relay-profiling/src/lib.rs`:

- `MAX_PROFILE_DURATION` is **30s** (v1). The PHP SDK enforces the same 30s locally.
- `MAX_PROFILE_CHUNK_DURATION` is **66s** (v2). The Python SDK flushes every 60s
  (`PROFILE_BUFFER_SECONDS`), so 66s is the ceiling with slack.

The 66s ceiling is what lets the chunk lookup bound its index scan on
`start_ts` instead of needing a range index.

Relay also normalizes before storing, and we should match it: drop threads with
only one non-idle sample, reject samples referencing a missing stack, reject
stacks referencing a missing frame, and sort samples by timestamp.

Sampling frequency is **101 Hz** by default in both SDKs
(`DEFAULT_SAMPLING_FREQUENCY`), and Python caps stack depth at 128
(`MAX_STACK_DEPTH`).
