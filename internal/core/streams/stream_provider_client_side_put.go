package streams

import (
	"net/http"
	"sync"

	"github.com/launchdarkly/ld-relay/v6/config"
	"gopkg.in/launchdarkly/go-sdk-common.v2/ldlog"
	"gopkg.in/launchdarkly/go-server-sdk.v5/interfaces/ldstoretypes"
)

// This is the standard implementation of a stream for client-side/mobile SDKs that sends only "Put" events,
// and does not do flag evaluations for specific users. The behavior of this stream is that it sends one "Put"
// event on initial connection, and another "Put" every time there is a data update of any kind.

type clientSidePutStreamProvider struct {
	isJSClient bool
	sub        chan PutStreamClient
	unsub      chan PutStreamClient
	heartbeat  chan int
	update     chan int
	shutdown   chan int
	store      EnvStoreQueries
	closeOnce  sync.Once
}

type clientSidePutEnvStreamProvider struct {
	store     EnvStoreQueries
	heartbeat chan int
	update    chan int
	shutdown  chan int
}

type clientSidePutEnvStreamRepository struct {
	store EnvStoreQueries
}

const UPDATE = 0
const HEARTBEAT = 1
const SHUTDOWN = 2

type PutStreamClient struct {
	update    chan int
	heartbeat chan int
	close     chan int
}

func newPutStreamProvider(isJSClient bool) StreamProvider {
	provider := clientSidePutStreamProvider{
		isJSClient: isJSClient,
		sub:        make(chan PutStreamClient),
		unsub:      make(chan PutStreamClient),
		heartbeat:  make(chan int),
		update:     make(chan int, 5),
		shutdown:   make(chan int),
	}
	provider.start()
	return &provider
}

func (s *clientSidePutStreamProvider) start() {

	go (func() {
		// no locks because this thread is the only writer
		clients := make(map[PutStreamClient]bool)
		cleanup := func() {
			for client, _ := range clients {
				close(client.update)
				close(client.heartbeat)
				close(client.close)
			}
			close(s.sub)
			close(s.unsub)
			close(s.shutdown)
		}
		defer cleanup()
	LOOP:
		for {
			select {
			case client := <-s.sub:
				clients[client] = true
			case client := <-s.unsub:
				delete(clients, client)
				close(client.update)
				close(client.heartbeat)
				close(client.close)
			case <-s.update:
				for client := range clients {
					client.update <- 1
				}
			case <-s.heartbeat:
				for client := range clients {
					client.heartbeat <- 1
				}
			case <-s.shutdown:
				break LOOP
			default:
				for client := range clients {
					select {
					case <-client.close:
						delete(clients, client)
						close(client.update)
						close(client.heartbeat)
						close(client.close)
					default:
						continue
					}

				}
			}
		}
	})()

}

func (s *clientSidePutStreamProvider) RegisterClient(credential config.SDKCredential) (update chan int, heartbeat chan int, close chan int) {
	if key := s.validateCredential(credential); key != "" {
		c := PutStreamClient{
			// idk if these are good buffer sizes
			update:    make(chan int, 2),
			heartbeat: make(chan int, 2),
			close:     make(chan int, 1),
		}
		s.sub <- c
		// i couldnt get route_core_endpoints.go to see putstreamclient as a type
		// so this is what is happening
		return c.update, c.heartbeat, c.close
	}
	return nil, nil, nil
}

func (s *clientSidePutStreamProvider) validateCredential(credential config.SDKCredential) string {
	if s.isJSClient {
		if key, ok := credential.(config.EnvironmentID); ok {
			return string(key)
		}

	} else {
		if key, ok := credential.(config.MobileKey); ok {
			return string(key)
		}
	}
	return ""
}

func (s *clientSidePutStreamProvider) Handler(credential config.SDKCredential) http.HandlerFunc {
	return nil
}

func (s *clientSidePutStreamProvider) Register(
	credential config.SDKCredential,
	store EnvStoreQueries,
	loggers ldlog.Loggers,
) EnvStreamProvider {

	if key := s.validateCredential(credential); key != "" {

		envStream := &clientSidePutEnvStreamProvider{
			store:     store,
			update:    s.update,
			shutdown:  s.shutdown,
			heartbeat: s.heartbeat,
		}
		return envStream
	}
	return nil
}

func (s *clientSidePutStreamProvider) Close() {
	s.closeOnce.Do(func() {
		close(s.shutdown)
	})
}

func (e *clientSidePutEnvStreamProvider) SendAllDataUpdate(allData []ldstoretypes.Collection) {
	e.update <- 1
}

func (e *clientSidePutEnvStreamProvider) SendSingleItemUpdate(kind ldstoretypes.DataKind, key string, item ldstoretypes.ItemDescriptor) {
	e.update <- 1
}

func (e *clientSidePutEnvStreamProvider) SendHeartbeat() {
	e.heartbeat <- 1
}

func (e *clientSidePutEnvStreamProvider) Close() {
	e.shutdown <- 1
}
