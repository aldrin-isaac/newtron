# RCA-006: SSHTunnel.Close() deadlock

## Symptom

`SSHTunnel.Close()` hung indefinitely. The goroutine calling `Close()` never
returned, blocking test cleanup and device disconnect operations.

## Root Cause

The original `Close()` implementation called `wg.Wait()` before closing the
SSH client. The forwarding goroutines were blocked on `io.Copy` reading from
the SSH channel. Since the SSH client was still open, these reads never
returned — and `wg.Wait()` waited for the goroutines that were blocked on
reads that would never complete.

```go
// DEADLOCK:
func (t *SSHTunnel) Close() {
    t.listener.Close()
    t.wg.Wait()       // blocks forever — goroutines still reading
    t.sshClient.Close() // never reached
}
```

## Fix

Close the SSH client **before** `wg.Wait()`. Closing the SSH client
terminates all SSH channels, which unblocks the `io.Copy` reads in the
forwarding goroutines, allowing them to return and decrement the WaitGroup.

```go
// CORRECT:
func (t *SSHTunnel) Close() {
    t.listener.Close()
    t.sshClient.Close() // unblocks forwarding goroutines
    t.wg.Wait()         // now completes promptly
}
```

## Lesson

When closing a resource that has goroutines blocked on I/O, close the
underlying transport **before** waiting for goroutines to finish. The close
propagates as read/write errors that unblock the goroutines. Waiting before
closing creates a deadlock: you're waiting for goroutines that are waiting
for the resource you haven't closed yet.

This pattern applies broadly to any producer-consumer with blocking I/O:
close the source, then wait for consumers.
