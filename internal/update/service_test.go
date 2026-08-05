package update

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"tg-search/internal/db"
	"tg-search/internal/model"
	"tg-search/internal/repository"
	"tg-search/internal/retry"
)

func TestServiceProcessesQueuedEventsSequentiallyAndFlushesOnStop(t *testing.T) {
	processor := &recordingProcessor{}
	service := NewService(ServiceOptions{
		Processor: processor,
		QueueSize: 4,
	})

	ctx := context.Background()
	service.Start(ctx)
	for i := int64(1); i <= 3; i++ {
		if err := service.Enqueue(ctx, Event{Type: EventNewMessage, MessageID: i}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}

	got := processor.ids()
	want := []int64{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("processed ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("processed ids = %v, want %v", got, want)
		}
	}
}

func TestServiceFlushesStopQueueWithLiveProcessorContext(t *testing.T) {
	processor := &gatedContextProcessor{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		result:  make(chan error, 1),
	}
	service := NewService(ServiceOptions{
		Processor: processor,
		QueueSize: 1,
	})

	ctx := context.Background()
	service.Start(ctx)
	if err := service.Enqueue(ctx, Event{Type: EventNewMessage, MessageID: 1}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	select {
	case <-processor.entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for processor to start")
	}

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- service.Stop(context.Background())
	}()
	waitForServiceStopped(t, service)
	close(processor.release)

	select {
	case err := <-processor.result:
		if err != nil {
			t.Fatalf("processor context error during stop flush = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for processor result")
	}
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Stop")
	}
}

func TestServiceRetriesListenerAndMarksAccountReconnecting(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	account, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}

	listener := &flakyListener{started: make(chan struct{}, 2)}
	service := NewService(ServiceOptions{
		Accounts:      accounts,
		Processor:     &recordingProcessor{},
		Listener:      listener,
		RetryInterval: time.Millisecond,
	})
	service.Start(ctx)
	if err := service.StartAccount(ctx, account); err != nil {
		t.Fatalf("StartAccount returned error: %v", err)
	}

	waitForStarts(t, listener.started, 2)
	updated, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find updated account: %v", err)
	}
	if updated.Status != model.AccountStatusReconnecting {
		t.Fatalf("status = %q, want RECONNECTING", updated.Status)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestServiceRetriesListenerOnGotdPongMissedDisconnect(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	account, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}

	listener := &pongMissedListener{started: make(chan struct{}, 2)}
	service := NewService(ServiceOptions{
		Accounts:  accounts,
		Processor: &recordingProcessor{},
		Listener:  listener,
		RetryPolicy: retry.Policy{
			BaseDelay: time.Millisecond,
			MaxDelay:  time.Millisecond,
			MaxTries:  3,
			Sleep:     func(context.Context, time.Duration) error { return nil },
		},
	})
	service.Start(ctx)
	if err := service.StartAccount(ctx, account); err != nil {
		t.Fatalf("StartAccount returned error: %v", err)
	}

	// The pong-missed disconnect must be treated as transient, so the
	// listener is retried (started twice) and the account is marked
	// reconnecting rather than permanently abandoned.
	waitForStarts(t, listener.started, 2)
	updated, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find updated account: %v", err)
	}
	if updated.Status != model.AccountStatusReconnecting {
		t.Fatalf("status = %q, want RECONNECTING after transient disconnect", updated.Status)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestServiceStartsListenerOnlyWhenAccountHasListenEnabledChannel(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	channels := repository.NewChannelRepository(conn)
	disabledAccountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	enabledAccountID, _ := accounts.Save(ctx, model.Account{Phone: "+10000000001", Status: model.AccountStatusOnline})
	_, _ = channels.Save(ctx, model.Channel{AccountID: disabledAccountID, TelegramChannelID: 10, Title: "Disabled", Type: model.ChannelTypeChannel, ListenEnabled: false})
	_, _ = channels.Save(ctx, model.Channel{AccountID: enabledAccountID, TelegramChannelID: 20, Title: "Enabled", Type: model.ChannelTypeChannel, ListenEnabled: true})
	disabledAccount, _ := accounts.FindByID(ctx, disabledAccountID)
	enabledAccount, _ := accounts.FindByID(ctx, enabledAccountID)

	listener := &blockingStartedListener{started: make(chan int64, 1)}
	service := NewService(ServiceOptions{
		Accounts:  accounts,
		Channels:  channels,
		Processor: &recordingProcessor{},
		Listener:  listener,
	})
	service.Start(ctx)
	if err := service.StartAccount(ctx, disabledAccount); err != nil {
		t.Fatalf("StartAccount disabled returned error: %v", err)
	}
	select {
	case accountID := <-listener.started:
		t.Fatalf("listener started for account %d, want no start for disabled account", accountID)
	case <-time.After(20 * time.Millisecond):
	}

	if err := service.StartAccount(ctx, enabledAccount); err != nil {
		t.Fatalf("StartAccount enabled returned error: %v", err)
	}
	select {
	case accountID := <-listener.started:
		if accountID != enabledAccountID {
			t.Fatalf("listener account id = %d, want %d", accountID, enabledAccountID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for enabled listener start")
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestServiceMarksReconnectingAccountOnlineAfterListenerStabilizes(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusReconnecting})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	account, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}

	listener := &blockingStartedListener{started: make(chan int64, 1)}
	service := NewService(ServiceOptions{
		Accounts:    accounts,
		Processor:   &recordingProcessor{},
		Listener:    listener,
		OnlineDelay: time.Millisecond,
	})
	service.Start(ctx)
	if err := service.StartAccount(ctx, account); err != nil {
		t.Fatalf("StartAccount returned error: %v", err)
	}
	select {
	case <-listener.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for listener start")
	}
	updated, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find updated account: %v", err)
	}
	if updated.Status != model.AccountStatusReconnecting {
		t.Fatalf("status immediately after listener start = %q, want RECONNECTING", updated.Status)
	}
	waitForAccountStatus(t, accounts, accountID, model.AccountStatusOnline)
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestServiceKeepsReconnectingAccountReconnectingUntilRetryStabilizes(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusReconnecting})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	account, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}

	listener := &failThenBlockListener{
		started:       make(chan int, 2),
		releaseSecond: make(chan struct{}),
	}
	service := NewService(ServiceOptions{
		Accounts:    accounts,
		Processor:   &recordingProcessor{},
		Listener:    listener,
		OnlineDelay: time.Hour,
		RetryPolicy: retry.Policy{
			BaseDelay: time.Millisecond,
			MaxDelay:  time.Millisecond,
			MaxTries:  3,
			Sleep:     func(context.Context, time.Duration) error { return nil },
		},
	})
	service.Start(ctx)
	if err := service.StartAccount(ctx, account); err != nil {
		t.Fatalf("StartAccount returned error: %v", err)
	}

	waitForRun(t, listener.started, 1)
	waitForRun(t, listener.started, 2)
	updated, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find updated account: %v", err)
	}
	if updated.Status != model.AccountStatusReconnecting {
		t.Fatalf("status = %q, want RECONNECTING", updated.Status)
	}

	close(listener.releaseSecond)
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestServiceMarksAccountOnlineAfterReconnectSucceeds(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	account, _ := accounts.FindByID(ctx, accountID)

	listener := &reconnectThenSuccessListener{started: make(chan struct{}, 2)}
	service := NewService(ServiceOptions{
		Accounts:  accounts,
		Processor: &recordingProcessor{},
		Listener:  listener,
		RetryPolicy: retry.Policy{
			BaseDelay: time.Millisecond,
			MaxDelay:  time.Millisecond,
			MaxTries:  2,
			Sleep:     func(context.Context, time.Duration) error { return nil },
		},
	})
	service.Start(ctx)
	if err := service.StartAccount(ctx, account); err != nil {
		t.Fatalf("StartAccount returned error: %v", err)
	}
	waitForStarts(t, listener.started, 2)
	deadline := time.After(time.Second)
	for {
		updated, err := accounts.FindByID(ctx, accountID)
		if err != nil {
			t.Fatalf("find account: %v", err)
		}
		if updated.Status == model.AccountStatusOnline {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("status = %q, want ONLINE", updated.Status)
		case <-time.After(time.Millisecond):
		}
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestServiceMarksFloodWaitAndRetriesListener(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	account, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}

	listener := &floodWaitListener{started: make(chan struct{}, 2)}
	var slept []time.Duration
	service := NewService(ServiceOptions{
		Accounts:  accounts,
		Processor: &recordingProcessor{},
		Listener:  listener,
		RetryPolicy: retry.Policy{
			BaseDelay: time.Millisecond,
			MaxDelay:  time.Millisecond,
			MaxTries:  3,
			Sleep: func(ctx context.Context, d time.Duration) error {
				slept = append(slept, d)
				return nil
			},
		},
	})
	service.Start(ctx)
	if err := service.StartAccount(ctx, account); err != nil {
		t.Fatalf("StartAccount returned error: %v", err)
	}

	waitForStarts(t, listener.started, 2)
	updated, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find updated account: %v", err)
	}
	if updated.Status != model.AccountStatusFloodWait {
		t.Fatalf("status = %q, want FLOOD_WAIT", updated.Status)
	}
	if len(slept) != 1 || slept[0] != time.Millisecond {
		t.Fatalf("slept = %v, want capped 1ms flood wait", slept)
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

func TestServiceMarksAccountLoginRequiredOnAuthKeyUnregistered(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	accounts := repository.NewAccountRepository(conn)
	accountID, err := accounts.Save(ctx, model.Account{Phone: "+10000000000", Status: model.AccountStatusOnline})
	if err != nil {
		t.Fatalf("save account: %v", err)
	}
	account, err := accounts.FindByID(ctx, accountID)
	if err != nil {
		t.Fatalf("find account: %v", err)
	}

	listener := &authKeyUnregisteredListener{started: make(chan struct{}, 2)}
	service := NewService(ServiceOptions{
		Accounts:  accounts,
		Processor: &recordingProcessor{},
		Listener:  listener,
		RetryPolicy: retry.Policy{
			BaseDelay: time.Millisecond,
			MaxDelay:  time.Millisecond,
			MaxTries:  3,
			Sleep: func(context.Context, time.Duration) error {
				t.Fatal("Sleep called for auth failure")
				return nil
			},
		},
	})
	service.Start(ctx)
	if err := service.StartAccount(ctx, account); err != nil {
		t.Fatalf("StartAccount returned error: %v", err)
	}

	waitForStarts(t, listener.started, 1)
	waitForAccountStatus(t, accounts, accountID, model.AccountStatusLoginRequired)
	select {
	case <-listener.started:
		t.Fatal("listener retried after auth failure")
	case <-time.After(20 * time.Millisecond):
	}
	if err := service.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
}

type recordingProcessor struct {
	mu        sync.Mutex
	events    []Event
	processWG sync.WaitGroup
}

func (r *recordingProcessor) Process(ctx context.Context, event Event) error {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
	return nil
}

func (r *recordingProcessor) ids() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int64, 0, len(r.events))
	for _, event := range r.events {
		out = append(out, event.MessageID)
	}
	return out
}

type gatedContextProcessor struct {
	entered chan struct{}
	release chan struct{}
	result  chan error
	once    sync.Once
}

func (p *gatedContextProcessor) Process(ctx context.Context, event Event) error {
	p.once.Do(func() {
		close(p.entered)
	})
	<-p.release
	err := ctx.Err()
	p.result <- err
	return err
}

type flakyListener struct {
	mu      sync.Mutex
	runs    int
	started chan struct{}
}

func (l *flakyListener) Run(ctx context.Context, account model.Account, emit func(Event) error) error {
	l.mu.Lock()
	l.runs++
	run := l.runs
	l.mu.Unlock()
	l.started <- struct{}{}
	if run == 1 {
		return errors.New("temporary disconnect")
	}
	<-ctx.Done()
	return ctx.Err()
}

type floodWaitListener struct {
	mu      sync.Mutex
	runs    int
	started chan struct{}
}

type pongMissedListener struct {
	mu      sync.Mutex
	runs    int
	started chan struct{}
}

func (l *pongMissedListener) Run(ctx context.Context, account model.Account, emit func(Event) error) error {
	l.mu.Lock()
	l.runs++
	run := l.runs
	l.mu.Unlock()
	l.started <- struct{}{}
	if run == 1 {
		// Mirrors the gotd error when the ping loop misses a pong: a wrapped
		// context.DeadlineExceeded that must be treated as transient.
		return fmt.Errorf("group: task pingLoop: disconnect (pong missed): %w", context.DeadlineExceeded)
	}
	<-ctx.Done()
	return ctx.Err()
}

type blockingStartedListener struct {
	started chan int64
}

func (l *blockingStartedListener) Run(ctx context.Context, account model.Account, emit func(Event) error) error {
	l.started <- account.ID
	<-ctx.Done()
	return ctx.Err()
}

type reconnectThenSuccessListener struct {
	mu      sync.Mutex
	runs    int
	started chan struct{}
}

type failThenBlockListener struct {
	mu            sync.Mutex
	runs          int
	started       chan int
	releaseSecond chan struct{}
}

type authKeyUnregisteredListener struct {
	started chan struct{}
}

func (l *reconnectThenSuccessListener) Run(ctx context.Context, account model.Account, emit func(Event) error) error {
	l.mu.Lock()
	l.runs++
	run := l.runs
	l.mu.Unlock()
	l.started <- struct{}{}
	if run == 1 {
		return errors.New("temporary disconnect")
	}
	return nil
}

func (l *failThenBlockListener) Run(ctx context.Context, account model.Account, emit func(Event) error) error {
	l.mu.Lock()
	l.runs++
	run := l.runs
	l.mu.Unlock()
	l.started <- run
	if run == 1 {
		return errors.New("temporary disconnect")
	}
	select {
	case <-l.releaseSecond:
		return errors.New("temporary disconnect")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *authKeyUnregisteredListener) Run(ctx context.Context, account model.Account, emit func(Event) error) error {
	l.started <- struct{}{}
	return errors.New("callback: rpcDoRequest: rpc error code 401: AUTH_KEY_UNREGISTERED")
}

func (l *floodWaitListener) Run(ctx context.Context, account model.Account, emit func(Event) error) error {
	l.mu.Lock()
	l.runs++
	run := l.runs
	l.mu.Unlock()
	l.started <- struct{}{}
	if run == 1 {
		return retry.FloodWait(60, errors.New("FLOOD_WAIT_60"))
	}
	<-ctx.Done()
	return ctx.Err()
}

func waitForRun(t *testing.T, ch <-chan int, want int) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("listener run = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for listener run %d", want)
	}
}

func waitForAccountStatus(t *testing.T, accounts *repository.AccountRepository, accountID int64, want string) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			account, err := accounts.FindByID(context.Background(), accountID)
			if err != nil {
				t.Fatalf("find account: %v", err)
			}
			t.Fatalf("status = %q, want %s", account.Status, want)
		case <-ticker.C:
			account, err := accounts.FindByID(context.Background(), accountID)
			if err != nil {
				t.Fatalf("find account: %v", err)
			}
			if account.Status == want {
				return
			}
		}
	}
}

func waitForStarts(t *testing.T, ch <-chan struct{}, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for listener start %d", i+1)
		}
	}
}

func waitForServiceStopped(t *testing.T, service *Service) {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for service to stop")
		case <-ticker.C:
			service.mu.Lock()
			stopped := service.stopped
			service.mu.Unlock()
			if stopped {
				return
			}
		}
	}
}
