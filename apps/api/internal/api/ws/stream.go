package ws

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

// ServeStream upgrades the request to a WebSocket and writes every []byte that
// arrives on src to the client until the connection closes or ctx is done. It is
// a one-way server→browser push channel (the notification live feed); any client
// message is ignored beyond detecting disconnect. onClose runs exactly once when
// the connection ends, so callers can unsubscribe from their source.
func ServeStream(w http.ResponseWriter, r *http.Request, src <-chan []byte, onClose func()) {
	conn, err := websocket.Accept(w, r, acceptOptions())
	if err != nil {
		if onClose != nil {
			onClose()
		}
		return
	}
	defer conn.CloseNow()
	if onClose != nil {
		defer onClose()
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Reader pump: we don't expect input, but reading lets us notice the client
	// going away promptly and cancel the writer.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		case msg, ok := <-src:
			if !ok {
				return
			}
			wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Write(wctx, websocket.MessageText, msg)
			wcancel()
			if err != nil {
				return
			}
		}
	}
}
