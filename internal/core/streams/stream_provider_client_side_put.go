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
	heartbeat  chan struct{}
	update     chan struct{}
	shutdown   chan struct{}
	store      EnvStoreQueries
	closeOnce  sync.Once
}

type clientSidePutEnvStreamProvider struct {
	store     EnvStoreQueries
	heartbeat chan struct{}
	update    chan struct{}
	shutdown  chan struct{}
}

type clientSidePutEnvStreamRepository struct {
	store EnvStoreQueries
}

type PutStreamClient struct {
	update    chan struct{}
	heartbeat chan struct{}
	close     chan struct{}
}

func newPutStreamProvider(isJSClient bool) StreamProvider {
	provider := clientSidePutStreamProvider{
		isJSClient: isJSClient,
		// buffer subs so that bursts of new clients
		// dont hang as much
		sub:       make(chan PutStreamClient, 255),
		unsub:     make(chan PutStreamClient),
		heartbeat: make(chan struct{}),
		update:    make(chan struct{}, 5),
		shutdown:  make(chan struct{}),
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
					client.update <- struct{}{}
				}
			case <-s.heartbeat:
				for client := range clients {
					client.heartbeat <- struct{}{}
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

func (s *clientSidePutStreamProvider) RegisterClient(credential config.SDKCredential) (update chan struct{}, heartbeat chan struct{}, close chan struct{}) {
	if key := s.validateCredential(credential); key != "" {
		c := PutStreamClient{
			// idk if these are good buffer sizes
			update:    make(chan struct{}, 5),
			heartbeat: make(chan struct{}, 5),
			close:     make(chan struct{}, 1),
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
	e.update <- struct{}{}
}

func (e *clientSidePutEnvStreamProvider) SendSingleItemUpdate(kind ldstoretypes.DataKind, key string, item ldstoretypes.ItemDescriptor) {
	e.update <- struct{}{}
}

func (e *clientSidePutEnvStreamProvider) SendHeartbeat() {
	e.heartbeat <- struct{}{}
}

func (e *clientSidePutEnvStreamProvider) Close() {
	e.shutdown <- struct{}{}
}
