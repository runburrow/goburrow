# Resource usage

Measured 2026-11-07 on commit `97f1d01`, release build (`-trimpath -ldflags "-s -w"`).

**Machine:** Intel Core i5-1235U laptop, 22 GB RAM, SATA SSD, Linux. The server was
pinned to the cores listed with `taskset`, and the load generator ran on separate
cores.

**Events:** realistic Python SDK events of about 4.5 KB of JSON (1.1 KB gzipped on
the wire). Each has a 3-frame stack trace with source context and local variables,
12 breadcrumbs, tags, user, request and contexts. They're sent as gzipped envelopes
spread over 200 or 2,000 distinct issues. Smaller events, like a message with no
stack, use less of everything.

## Summary

| | Measured |
|---|---|
| Binary | 14.0 MiB (6.1 MB gzipped) |
| Startup to serving | ~80 ms |
| Memory, idle | ~15 MiB |
| Memory, ingesting at full speed | 25–30 MiB |
| Memory, 300k events stored, dashboard in use | ~45 MiB |
| CPU per event | 0.4–0.9 ms |
| Throughput, 1 core | ~1,750 events/s |
| Throughput, 2+ cores | ~2,150 events/s |
| Disk per stored event | ~5.1 KiB |
| Retention cleanup | ~150k events in ~3 s on 1 core |

Writes go through one SQLite writer, so extra cores add little throughput. A
steady 50 events/s, which is already a busy app, uses about 3% of one core.

## Ingestion

| Server cores | Load | Events/s | p50 | p95 | p99 | CPU used | Peak memory |
|---|---|---|---|---|---|---|---|
| 1 | full speed, 8 clients | 1,774 | 2.4 ms | 16 ms | 20 ms | 0.76 cores | 27.6 MiB |
| 1 | steady 50/s | 50 | 0.8 ms | 1.1 ms | 9 ms | 0.03 cores | 21.8 MiB |
| 2 | full speed, 16 clients | 2,163 | 4.8 ms | 16 ms | 39 ms | 1.60 cores | 29.2 MiB |
| 4 | full speed, 16 clients, 140 s | 2,177 | 3.9 ms | 14 ms | 59 ms | 2.03 cores | 41.1 MiB |

## Memory limits

The server ran on one core inside a cgroup with a hard memory limit and no swap,
which is what a container memory limit does.

| Limit | Load | Result |
|---|---|---|
| 64 MB | full speed, 25 s | ran, 1,199 events/s |
| 48 MB | full speed, 25 s | ran, 1,174 events/s |
| 32 MB | full speed, 25 s | ran, 1,144 events/s |
| 24 MB | steady 50/s, 30 s | ran |

A limit leaves less room for the OS file cache, so full-speed ingestion drops by
about a third under 64 MB. Normal traffic doesn't notice.

Keep the database on a real disk. On a RAM-backed filesystem such as `/tmp` on many
distributions, the database counts against the memory limit and grows until the
process is killed.

## Disk

About 5.1 KiB per stored event, a steady figure from 1,500 to 305,000 events.
Issues add almost nothing. Expect roughly:

| Events per day | 30-day retention | 90-day retention |
|---|---|---|
| 1,000 | 150 MB | 460 MB |
| 10,000 | 1.5 GB | 4.6 GB |
| 100,000 | 15 GB | 46 GB |

Retention runs at startup and hourly. Removing 152,508 events (about 800 MB) took
2.7 s on one core, while 50 events/s kept arriving (p99 26 ms). The space is
reused for new events, but the file doesn't shrink. To give it back to the OS,
stop Burrow and run `sqlite3 burrow.db VACUUM`.

## Dashboard

These were measured with 305,017 events across 2,000 issues (a 1.6 GB database) and
a warm OS cache. The times are server response times.

| Page | Median |
|---|---|
| Projects list | 126 ms |
| Issue list | 7.5 ms |
| Issue list, sorted by events | 9.6 ms |
| Search | 3.1 ms |
| Issue detail | 2.5 ms |
| Project settings | 0.7 ms |

The projects list draws 14 days of activity per project from the events table, so
its time grows with recent event volume. Every other page stays flat.

## Not measured

- **Container image:** Docker wasn't available on the test machine. The image is
  the binary on `distroless/static`, so expect it to be a few MB larger than the binary.
- **arm64, macOS, Windows:** Burrow is pure Go with no cgo, so it builds for them,
  but they weren't benchmarked.
- **Spinning disks and network storage:** expect lower ingestion throughput on a
  hard disk. Don't put the database on NFS or SMB, because SQLite's locking isn't
  reliable there.

## Reproducing

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o burrow ./cmd/burrow
taskset -c 0 ./burrow -db bench.db -retention-days 0 -rate-limit 0 &
# create a project in the dashboard, then:
go run ./tools/loadgen -url http://127.0.0.1:8000 -project 1 -key <public key> \
  -pid $(pgrep -x burrow) -dur 20s -conc 8 -issues 200
```

`-rate-limit 0` turns off the per-project ingestion limit (3,000 events a minute by
default), which full-speed runs would otherwise hit within seconds. Add `-rate 50` to the
load generator for a steady rate instead of full speed. For a memory limit, start the
server with `systemd-run --user --scope -p MemoryMax=32M -p MemorySwapMax=0 ./burrow …`.
