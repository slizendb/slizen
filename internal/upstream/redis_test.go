package upstream

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisClientGetPipelinesGETAndPTTL(t *testing.T) {
	binaryValue := []byte{0x00, 0x01, 0xff, '\r', '\n'}

	tests := []struct {
		name        string
		getReply    []byte
		pttlReply   []byte
		want        Value
		wantErrText string
	}{
		{
			name:      "binary value",
			getReply:  respBulk(binaryValue),
			pttlReply: []byte(":2500\r\n"),
			want:      Value{Data: binaryValue, Exists: true, PTTL: 2500 * time.Millisecond},
		},
		{
			name:      "missing key",
			getReply:  []byte("$-1\r\n"),
			pttlReply: []byte(":-2\r\n"),
			want:      Value{Exists: false},
		},
		{
			name:      "key expires between commands",
			getReply:  respBulk([]byte("value")),
			pttlReply: []byte(":-2\r\n"),
			want:      Value{Exists: false},
		},
		{
			name:      "key without expiration",
			getReply:  respBulk([]byte("value")),
			pttlReply: []byte(":-1\r\n"),
			want:      Value{Data: []byte("value"), Exists: true, PTTL: time.Duration(-1)},
		},
		{
			name:      "missing key ignores PTTL error",
			getReply:  []byte("$-1\r\n"),
			pttlReply: []byte("-ERR pttl failed\r\n"),
			want:      Value{Exists: false},
		},
		{
			name:        "GET error",
			getReply:    []byte("-ERR get failed\r\n"),
			pttlReply:   []byte(":-1\r\n"),
			wantErrText: "get failed",
		},
		{
			name:        "PTTL error",
			getReply:    respBulk([]byte("value")),
			pttlReply:   []byte("-ERR pttl failed\r\n"),
			wantErrText: "pttl failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "test-key"
			client, serverDone := newScriptedRedisClient(t, []redisCommand{
				{args: []string{"get", key}, reply: tt.getReply},
				{args: []string{"pttl", key}, reply: tt.pttlReply},
			})

			got, err := client.Get(context.Background(), key)
			if serverErr := <-serverDone; serverErr != nil {
				t.Fatalf("fake Redis server: %v", serverErr)
			}
			if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("Get() error = %v, want error containing %q", err, tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if got.Exists != tt.want.Exists || got.PTTL != tt.want.PTTL || !bytes.Equal(got.Data, tt.want.Data) {
				t.Fatalf("Get() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRedisClientGetReturnsCanceledContext(t *testing.T) {
	client := &RedisClient{client: redis.NewClient(&redis.Options{
		Dialer: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return nil, ctx.Err()
		},
		MaxRetries:      -1,
		DisableIdentity: true,
	})}
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := client.Get(ctx, "test-key")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Get() error = %v, want context.Canceled", err)
	}
	if got.Exists || got.PTTL != 0 || got.Data != nil {
		t.Fatalf("Get() = %+v, want zero Value", got)
	}
}

func TestRedisClientGetMissingKeyPropagatesPipelineExecutionFailure(t *testing.T) {
	const key = "test-key"

	t.Run("context canceled after GET reply", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client, getReplied, releaseServer, serverDone := newPhasedMissingRedisClient(t, true, nil, key)

		result := callRedisGet(client, ctx, key)
		<-getReplied
		cancel()
		got := <-result
		releaseServer()
		if serverErr := <-serverDone; serverErr != nil {
			t.Fatalf("fake Redis server: %v", serverErr)
		}
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("Get() error = %v, want context.Canceled", got.err)
		}
		if got.value.Exists || got.value.PTTL != 0 || got.value.Data != nil {
			t.Fatalf("Get() = %+v, want zero Value", got.value)
		}
	})

	t.Run("deadline exceeded after GET reply", func(t *testing.T) {
		client, getReplied, releaseServer, serverDone := newPhasedMissingRedisClient(t, false, context.DeadlineExceeded, key)

		result := callRedisGet(client, context.Background(), key)
		<-getReplied
		releaseServer()
		got := <-result
		if serverErr := <-serverDone; serverErr != nil {
			t.Fatalf("fake Redis server: %v", serverErr)
		}
		if !errors.Is(got.err, context.DeadlineExceeded) {
			t.Fatalf("Get() error = %v, want context.DeadlineExceeded", got.err)
		}
		if got.value.Exists || got.value.PTTL != 0 || got.value.Data != nil {
			t.Fatalf("Get() = %+v, want zero Value", got.value)
		}
	})

	t.Run("connection closes after GET reply", func(t *testing.T) {
		client, getReplied, releaseServer, serverDone := newPhasedMissingRedisClient(t, false, nil, key)

		result := callRedisGet(client, context.Background(), key)
		<-getReplied
		releaseServer()
		got := <-result
		if serverErr := <-serverDone; serverErr != nil {
			t.Fatalf("fake Redis server: %v", serverErr)
		}
		if !errors.Is(got.err, io.EOF) {
			t.Fatalf("Get() error = %v, want io.EOF", got.err)
		}
		if got.value.Exists || got.value.PTTL != 0 || got.value.Data != nil {
			t.Fatalf("Get() = %+v, want zero Value", got.value)
		}
	})
}

func TestRedisClientMGetTreatsPTTLMinusTwoAsMissing(t *testing.T) {
	client, serverDone := newScriptedRedisClient(t, []redisCommand{
		{
			args:  []string{"mget", "expired", "present"},
			reply: []byte("*2\r\n$5\r\nvalue\r\n$5\r\nvalue\r\n"),
		},
		{args: []string{"pttl", "expired"}, reply: []byte(":-2\r\n")},
		{args: []string{"pttl", "present"}, reply: []byte(":1000\r\n")},
	})

	got, err := client.MGet(context.Background(), []string{"expired", "present"})
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatalf("fake Redis server: %v", serverErr)
	}
	if err != nil {
		t.Fatalf("MGet() error = %v", err)
	}
	want := []Value{
		{Exists: false},
		{Data: []byte("value"), Exists: true, PTTL: time.Second},
	}
	if len(got) != len(want) {
		t.Fatalf("MGet() returned %d values, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Exists != want[i].Exists || got[i].PTTL != want[i].PTTL || !bytes.Equal(got[i].Data, want[i].Data) {
			t.Errorf("MGet()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

type redisCommand struct {
	args  []string
	reply []byte
}

type redisGetResult struct {
	value Value
	err   error
}

type mappedReadErrorConn struct {
	net.Conn
	mapError func() error
}

func (c *mappedReadErrorConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		if mappedErr := c.mapError(); mappedErr != nil {
			return n, mappedErr
		}
	}
	return n, err
}

func callRedisGet(client *RedisClient, ctx context.Context, key string) <-chan redisGetResult {
	result := make(chan redisGetResult, 1)
	go func() {
		value, err := client.Get(ctx, key)
		result <- redisGetResult{value: value, err: err}
	}()
	return result
}

func newPhasedMissingRedisClient(t *testing.T, cancelAware bool, readFailure error, key string) (*RedisClient, <-chan struct{}, func(), <-chan error) {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	dialed := make(chan struct{}, 1)
	client := &RedisClient{client: redis.NewClient(&redis.Options{
		Dialer: func(ctx context.Context, _, _ string) (net.Conn, error) {
			select {
			case dialed <- struct{}{}:
			default:
				return nil, errors.New("unexpected second Redis connection")
			}
			if cancelAware {
				go func() {
					<-ctx.Done()
					_ = clientConn.SetReadDeadline(time.Now())
				}()
				return &mappedReadErrorConn{Conn: clientConn, mapError: ctx.Err}, nil
			}
			if readFailure != nil {
				return &mappedReadErrorConn{Conn: clientConn, mapError: func() error { return readFailure }}, nil
			}
			return clientConn, nil
		},
		Protocol:        2,
		MaxRetries:      -1,
		ReadTimeout:     2 * time.Second,
		WriteTimeout:    2 * time.Second,
		PoolSize:        1,
		DisableIdentity: true,
	})}
	getReplied := make(chan struct{})
	release := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- servePhasedMissing(serverConn, key, getReplied, release)
	}()
	var releaseOnce sync.Once
	releaseServer := func() {
		releaseOnce.Do(func() { close(release) })
	}
	t.Cleanup(func() {
		releaseServer()
		_ = client.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return client, getReplied, releaseServer, serverDone
}

func newScriptedRedisClient(t *testing.T, commands []redisCommand) (*RedisClient, <-chan error) {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	dialed := make(chan struct{}, 1)
	client := &RedisClient{client: redis.NewClient(&redis.Options{
		Dialer: func(context.Context, string, string) (net.Conn, error) {
			select {
			case dialed <- struct{}{}:
				return clientConn, nil
			default:
				return nil, errors.New("unexpected second Redis connection")
			}
		},
		Protocol:        2,
		MaxRetries:      -1,
		ReadTimeout:     2 * time.Second,
		WriteTimeout:    2 * time.Second,
		PoolSize:        1,
		DisableIdentity: true,
	})}
	done := make(chan error, 1)
	go func() {
		done <- serveRedisCommands(serverConn, commands)
	}()
	t.Cleanup(func() {
		_ = client.Close()
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return client, done
}

func serveRedisCommands(conn net.Conn, commands []redisCommand) error {
	defer conn.Close()
	reader, writer, err := acceptRedisConnection(conn)
	if err != nil {
		return err
	}

	for i, command := range commands {
		got, err := readRESPCommand(reader)
		if err != nil {
			return fmt.Errorf("read command %d: %w", i, err)
		}
		if !equalCommand(got, command.args) {
			return fmt.Errorf("command %d = %q, want %q", i, got, command.args)
		}
	}
	for i, command := range commands {
		if _, err := writer.Write(command.reply); err != nil {
			return fmt.Errorf("write response %d: %w", i, err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush command responses: %w", err)
	}
	return nil
}

func servePhasedMissing(conn net.Conn, key string, getReplied chan<- struct{}, release <-chan struct{}) error {
	defer conn.Close()
	reader, writer, err := acceptRedisConnection(conn)
	if err != nil {
		return err
	}
	for i, want := range [][]string{{"get", key}, {"pttl", key}} {
		got, err := readRESPCommand(reader)
		if err != nil {
			return fmt.Errorf("read command %d: %w", i, err)
		}
		if !equalCommand(got, want) {
			return fmt.Errorf("command %d = %q, want %q", i, got, want)
		}
	}
	if _, err := writer.WriteString("$-1\r\n"); err != nil {
		return fmt.Errorf("write GET response: %w", err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush GET response: %w", err)
	}
	close(getReplied)
	<-release
	return nil
}

func acceptRedisConnection(conn net.Conn) (*bufio.Reader, *bufio.Writer, error) {
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, nil, err
	}

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	hello, err := readRESPCommand(reader)
	if err != nil {
		return nil, nil, fmt.Errorf("read HELLO: %w", err)
	}
	if len(hello) < 2 || !strings.EqualFold(hello[0], "hello") {
		return nil, nil, fmt.Errorf("first command = %q, want HELLO", hello)
	}
	if _, err := writer.WriteString("-ERR unknown command 'hello'\r\n"); err != nil {
		return nil, nil, fmt.Errorf("write HELLO response: %w", err)
	}
	if err := writer.Flush(); err != nil {
		return nil, nil, fmt.Errorf("flush HELLO response: %w", err)
	}
	return reader, writer, nil
}

func readRESPCommand(reader *bufio.Reader) ([]string, error) {
	header, err := readRESPLine(reader)
	if err != nil {
		return nil, err
	}
	if len(header) < 2 || header[0] != '*' {
		return nil, fmt.Errorf("invalid array header %q", header)
	}
	count, err := strconv.Atoi(header[1:])
	if err != nil || count < 0 {
		return nil, fmt.Errorf("invalid array length %q", header[1:])
	}

	args := make([]string, count)
	for i := range args {
		bulkHeader, err := readRESPLine(reader)
		if err != nil {
			return nil, err
		}
		if len(bulkHeader) < 2 || bulkHeader[0] != '$' {
			return nil, fmt.Errorf("invalid bulk header %q", bulkHeader)
		}
		length, err := strconv.Atoi(bulkHeader[1:])
		if err != nil || length < 0 {
			return nil, fmt.Errorf("invalid bulk length %q", bulkHeader[1:])
		}
		payload := make([]byte, length+2)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, err
		}
		if !bytes.Equal(payload[length:], []byte("\r\n")) {
			return nil, errors.New("bulk string missing CRLF")
		}
		args[i] = string(payload[:length])
	}
	return args, nil
}

func readRESPLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	if len(line) < 2 || !strings.HasSuffix(line, "\r\n") {
		return "", fmt.Errorf("invalid RESP line %q", line)
	}
	return line[:len(line)-2], nil
}

func equalCommand(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if i == 0 {
			if !strings.EqualFold(got[i], want[i]) {
				return false
			}
			continue
		}
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func respBulk(value []byte) []byte {
	reply := []byte(fmt.Sprintf("$%d\r\n", len(value)))
	reply = append(reply, value...)
	return append(reply, '\r', '\n')
}
