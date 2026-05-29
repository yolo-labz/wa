package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/spf13/cobra"
)

var (
	subscribeEvents     string
	subscribeChats      string
	subscribeSenders    string
	subscribeNotSenders string
	subscribeBodyRe     string
	subscribeSince      int64
)

var subscribeCmd = &cobra.Command{
	Use:   "subscribe",
	Short: "Stream events from the daemon (FR-060)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if subscribeEvents == "" {
			return exitf(64, "wa subscribe: --events is required")
		}

		params := map[string]any{
			"events": strings.Split(subscribeEvents, ","),
		}
		if subscribeChats != "" {
			params["chats"] = strings.Split(subscribeChats, ",")
		}
		if subscribeSenders != "" {
			params["senders"] = strings.Split(subscribeSenders, ",")
		}
		if subscribeNotSenders != "" {
			params["notSenders"] = strings.Split(subscribeNotSenders, ",")
		}
		if subscribeBodyRe != "" {
			params["bodyRe"] = subscribeBodyRe
		}
		if subscribeSince > 0 {
			params["since"] = subscribeSince
		}

		return runSubscribeStream(cmd, params)
	},
}

// runSubscribeStream opens the socket, sends a `subscribe` request with
// the given params, then streams notification frames until the daemon
// closes the subscription or the user interrupts (ctrl-C / SIGTERM),
// printing each event's params as one NDJSON line. It is shared by
// `wa subscribe` and the `wa stream` convenience wrapper (#174) so the
// stream transport lives in exactly one place.
func runSubscribeStream(cmd *cobra.Command, params map[string]any) error {
	conn, reader, subID, err := openSubscription(flagSocket, params)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	var closed atomic.Bool
	go func() {
		<-ctx.Done()
		closed.Store(true)
		_ = conn.Close()
	}()

	lines := make(chan []byte)
	readErrCh := make(chan error, 1)
	go func() {
		defer close(lines)
		for {
			line, err := reader.ReadBytes('\n')
			if len(line) > 0 {
				lines <- line
			}
			if err != nil {
				readErrCh <- err
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			sendUnsubscribe(conn, subID)
			return nil
		case line, ok := <-lines:
			if !ok {
				if closed.Load() {
					return nil
				}
				return exiterr(1, errors.New("subscribe: connection closed"))
			}
			if err := handleSubscribeLine(conn, line, subID); err != nil {
				return err
			}
		}
	}
}

// openSubscription dials the daemon, sends the `subscribe` request with
// the given params, and validates the response. On success it returns the
// live connection, a buffered reader positioned just past the response
// line (subsequent reads yield notification frames), and the
// server-assigned subscription id. Any failure closes the connection and
// returns an *exitError carrying the mapped exit code.
func openSubscription(socketPath string, params map[string]any) (net.Conn, *bufio.Reader, string, error) {
	conn, err := dial(socketPath)
	if err != nil {
		return nil, nil, "", exiterr(10, err)
	}

	subReq := rpcRequest{
		JSONRPC: "2.0",
		ID:      nextID.Add(1),
		Method:  "subscribe",
		Params:  params,
	}
	data, err := json.Marshal(subReq)
	if err != nil {
		_ = conn.Close()
		return nil, nil, "", exiterr(1, fmt.Errorf("marshal subscribe: %w", err))
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		_ = conn.Close()
		return nil, nil, "", exiterr(1, fmt.Errorf("write subscribe: %w", err))
	}

	reader := bufio.NewReader(conn)
	subLine, err := reader.ReadBytes('\n')
	if err != nil {
		_ = conn.Close()
		return nil, nil, "", exiterr(1, fmt.Errorf("read subscribe response: %w", err))
	}
	var subResp rpcResponse
	if err := json.Unmarshal(subLine, &subResp); err != nil {
		_ = conn.Close()
		return nil, nil, "", exiterr(1, fmt.Errorf("unmarshal subscribe response: %w", err))
	}
	if subResp.Error != nil {
		_ = conn.Close()
		return nil, nil, "", exiterr(rpcCodeToExit(subResp.Error.Code), subResp.Error)
	}
	var subResult struct {
		SubscriptionID string `json:"subscriptionId"`
	}
	if err := json.Unmarshal(subResp.Result, &subResult); err != nil {
		_ = conn.Close()
		return nil, nil, "", exiterr(1, fmt.Errorf("unmarshal subscribe result: %w", err))
	}
	if subResult.SubscriptionID == "" {
		_ = conn.Close()
		return nil, nil, "", exitf(1, "subscribe: daemon returned empty subscriptionId")
	}
	return conn, reader, subResult.SubscriptionID, nil
}

// sendUnsubscribe best-effort writes an `unsubscribe` request for subID so
// the daemon can release the subscription promptly on ctrl-C. Errors are
// ignored — the connection is about to close regardless.
func sendUnsubscribe(conn net.Conn, subID string) {
	unsubParams, _ := json.Marshal(map[string]any{"subscriptionId": subID})
	unsubReq := rpcRequest{
		JSONRPC: "2.0",
		ID:      nextID.Add(1),
		Method:  "unsubscribe",
		Params:  json.RawMessage(unsubParams),
	}
	if raw, err := json.Marshal(unsubReq); err == nil {
		_, _ = conn.Write(append(raw, '\n'))
	}
}

func handleSubscribeLine(conn interface{ Write(p []byte) (int, error) }, line []byte, subID string) error {
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(line, &frame); err != nil {
		return nil
	}

	if methodRaw, ok := frame["method"]; ok {
		var method string
		if err := json.Unmarshal(methodRaw, &method); err != nil {
			return nil
		}
		switch method {
		case "subscribe.ping":
			pongParams, _ := json.Marshal(map[string]any{"subscriptionId": subID})
			pongReq := rpcRequest{
				JSONRPC: "2.0",
				ID:      nextID.Add(1),
				Method:  "subscribe.pong",
				Params:  json.RawMessage(pongParams),
			}
			if raw, err := json.Marshal(pongReq); err == nil {
				_, _ = conn.Write(append(raw, '\n'))
			}
			return nil
		case "event":
			if paramsRaw, ok := frame["params"]; ok {
				fmt.Println(string(paramsRaw))
			}
			return nil
		default:
			fmt.Println(string(line[:len(line)-1]))
			return nil
		}
	}

	if errRaw, ok := frame["error"]; ok {
		var rpcErr rpcError
		if err := json.Unmarshal(errRaw, &rpcErr); err != nil {
			fmt.Fprintln(os.Stderr, string(line[:len(line)-1]))
			return nil
		}
		fmt.Fprintln(os.Stderr, string(line[:len(line)-1]))
		switch rpcErr.Code {
		case -32002: // ShutdownInProgress
			return exiterr(0, nil)
		case -32005: // SubscriptionClosed
			return exiterr(0, nil)
		case -32006: // PongTimeout
			return exiterr(12, &rpcErr)
		case -32007: // StreamDrop
			return nil
		}
	}
	return nil
}

func init() {
	subscribeCmd.Flags().StringVar(&subscribeEvents, "events", "", "comma-separated event types (required)")
	subscribeCmd.Flags().StringVar(&subscribeChats, "chats", "", "comma-separated chat JIDs")
	subscribeCmd.Flags().StringVar(&subscribeSenders, "senders", "", "comma-separated sender JIDs")
	subscribeCmd.Flags().StringVar(&subscribeNotSenders, "not-senders", "", "comma-separated sender JIDs to exclude")
	subscribeCmd.Flags().StringVar(&subscribeBodyRe, "body-re", "", "body regex filter")
	subscribeCmd.Flags().Int64Var(&subscribeSince, "since", 0, "resume from this seq (Kafka-style cursor)")
	rootCmd.AddCommand(subscribeCmd)
}
