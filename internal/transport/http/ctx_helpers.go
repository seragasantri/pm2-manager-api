package httpx

import "context"

// cancelOnErr membuat context turunan yang dibatalkan saat errCh menerima nilai
// (mis. pembaca WebSocket mendeteksi koneksi putus).
func cancelOnErr(parent context.Context, errCh <-chan error) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	go func() {
		select {
		case <-errCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}
