package update

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"

	"tg-search/internal/model"
	"tg-search/internal/repository"
	"tg-search/internal/retry"
)

var ErrServiceStopped = errors.New("update service stopped")

type EventProcessor interface {
	Process(context.Context, Event) error
}

type Listener interface {
	Run(ctx context.Context, account model.Account, emit func(Event) error) error
}

type ListenerFunc func(ctx context.Context, account model.Account, emit func(Event) error) error

func (f ListenerFunc) Run(ctx context.Context, account model.Account, emit func(Event) error) error {
	return f(ctx, account, emit)
}

type NopListener struct{}

func (NopListener) Run(context.Context, model.Account, func(Event) error) error {
	return nil
}

type ServiceOptions struct {
	Accounts      *repository.AccountRepository
	Channels      *repository.ChannelRepository
	Processor     EventProcessor
	Listener      Listener
	QueueSize     int
	RetryInterval time.Duration
	OnlineDelay   time.Duration
	RetryPolicy   retry.Policy
	Logger        *zap.Logger
}

type Service struct {
	accounts      *repository.AccountRepository
	channels      *repository.ChannelRepository
	processor     EventProcessor
	listener      Listener
	retryInterval time.Duration
	onlineDelay   time.Duration
	retryPolicy   retry.Policy
	logger        *zap.Logger

	mu              sync.Mutex
	startOnce       sync.Once
	listenerCancel  context.CancelFunc
	listenerCtx     context.Context
	listenerCancels map[int64]context.CancelFunc
	listenerTokens  map[int64]uint64
	nextToken       uint64
	processorCancel context.CancelFunc
	processorCtx    context.Context
	events          chan Event
	stopped         bool
	workerWG        sync.WaitGroup
	listenerWG      sync.WaitGroup
}

func NewService(opts ServiceOptions) *Service {
	if opts.Listener == nil {
		opts.Listener = NopListener{}
	}
	if opts.RetryInterval <= 0 {
		opts.RetryInterval = time.Second
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 100
	}
	if opts.OnlineDelay <= 0 {
		opts.OnlineDelay = 5 * time.Second
	}
	if opts.RetryPolicy.MaxTries == 0 && opts.RetryPolicy.BaseDelay == 0 && opts.RetryPolicy.MaxDelay == 0 && opts.RetryPolicy.Sleep == nil {
		opts.RetryPolicy = retry.Policy{
			BaseDelay: opts.RetryInterval,
			MaxDelay:  30 * time.Minute,
			MaxTries:  3,
		}
	}
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	return &Service{
		accounts:        opts.Accounts,
		channels:        opts.Channels,
		processor:       opts.Processor,
		listener:        opts.Listener,
		retryInterval:   opts.RetryInterval,
		onlineDelay:     opts.OnlineDelay,
		retryPolicy:     opts.RetryPolicy,
		logger:          opts.Logger,
		events:          make(chan Event, opts.QueueSize),
		listenerCancels: map[int64]context.CancelFunc{},
		listenerTokens:  map[int64]uint64{},
	}
}

func (s *Service) Start(ctx context.Context) {
	s.startOnce.Do(func() {
		s.listenerCtx, s.listenerCancel = context.WithCancel(ctx)
		s.processorCtx, s.processorCancel = context.WithCancel(context.WithoutCancel(ctx))
		s.workerWG.Add(1)
		go s.worker()
		s.logger.Info("update service started")
	})
}

func (s *Service) StartAccount(ctx context.Context, account model.Account) error {
	s.Start(ctx)
	if ok, err := s.hasListenEnabledChannels(ctx, account.ID); err != nil || !ok {
		if err != nil {
			s.logger.Error("check listen-enabled channels failed", zap.Int64("account_id", account.ID), zap.Error(err))
		} else {
			s.logger.Info("telegram update listener skipped because account has no listen-enabled channels", zap.Int64("account_id", account.ID))
		}
		return err
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return ErrServiceStopped
	}
	if _, ok := s.listenerCancels[account.ID]; ok {
		s.mu.Unlock()
		s.logger.Info("telegram update listener already running", zap.Int64("account_id", account.ID))
		return nil
	}
	accountCtx, cancel := context.WithCancel(s.listenerCtx)
	s.nextToken++
	token := s.nextToken
	s.listenerCancels[account.ID] = cancel
	s.listenerTokens[account.ID] = token
	s.listenerWG.Add(1)
	s.mu.Unlock()

	go s.runListener(accountCtx, token, account)
	s.logger.Info("telegram update listener starting", zap.Int64("account_id", account.ID))
	return nil
}

func (s *Service) Enqueue(ctx context.Context, event Event) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return ErrServiceStopped
	}
	events := s.events
	s.mu.Unlock()

	select {
	case events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) Stop(ctx context.Context) error {
	started := time.Now()
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	if s.listenerCancel != nil {
		s.listenerCancel()
	}
	for _, cancel := range s.listenerCancels {
		cancel()
	}
	s.mu.Unlock()

	if err := wait(ctx, &s.listenerWG); err != nil {
		s.logger.Error("wait for update listeners failed", zap.Error(err))
		return err
	}
	close(s.events)
	err := wait(ctx, &s.workerWG)
	if s.processorCancel != nil {
		s.processorCancel()
	}
	if err != nil {
		s.logger.Error("wait for update worker failed", zap.Error(err))
		return err
	}
	s.logger.Info("update service stopped", zap.Duration("duration", time.Since(started)))
	return err
}

func (s *Service) StopAccount(ctx context.Context, accountID int64) error {
	s.mu.Lock()
	cancel := s.listenerCancels[accountID]
	if cancel != nil {
		cancel()
		delete(s.listenerCancels, accountID)
		delete(s.listenerTokens, accountID)
		s.logger.Info("telegram update listener stop requested", zap.Int64("account_id", accountID))
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) worker() {
	defer s.workerWG.Done()
	for event := range s.events {
		if s.processor == nil {
			continue
		}
		if err := s.processor.Process(s.processorCtx, event); err != nil {
			s.logger.Error("process update event", zap.Error(err), zap.String("type", string(event.Type)))
		}
	}
}

func (s *Service) runListener(ctx context.Context, token uint64, account model.Account) {
	defer func() {
		s.mu.Lock()
		if s.listenerTokens[account.ID] == token {
			delete(s.listenerCancels, account.ID)
			delete(s.listenerTokens, account.ID)
		}
		s.mu.Unlock()
		s.listenerWG.Done()
	}()
	hadFailure := false
	for {
		s.logger.Info("telegram update listener running", zap.Int64("account_id", account.ID))
		err, markedOnline := s.runListenerAttempt(ctx, account)
		if markedOnline {
			account.Status = model.AccountStatusOnline
		}
		if ctx.Err() != nil {
			s.logger.Info("telegram update listener stopped by context", zap.Int64("account_id", account.ID))
			return
		}
		if err == nil {
			if hadFailure && s.accounts != nil {
				if updateErr := s.accounts.UpdateStatus(ctx, account.ID, model.AccountStatusOnline); updateErr != nil {
					s.logger.Error("mark account online after listener reconnect", zap.Int64("account_id", account.ID), zap.Error(updateErr))
				}
			}
			s.logger.Info("telegram update listener exited", zap.Int64("account_id", account.ID), zap.Bool("recovered", hadFailure))
			return
		}
		s.logger.Warn("telegram update listener stopped", zap.Int64("account_id", account.ID), zap.Error(err))
		classification := retry.Classify(err)
		// gotd surfaces transient connection drops — e.g. ping "pong missed"
		// and RPC "engine forcibly closed" — as wrapped context.Canceled /
		// context.DeadlineExceeded errors produced by its own internal
		// goroutines. The listener context was already checked above, so a
		// context error reaching here is gotd's connection lifecycle, not a
		// real shutdown: reconnect instead of treating it as a permanent
		// failure that stops syncing until a manual restart.
		if classification.Kind == retry.KindPermanent && ctx.Err() == nil &&
			(errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			classification.Kind = retry.KindTemporary
		}
		if classification.Kind == retry.KindAuth {
			if s.accounts != nil {
				if updateErr := s.accounts.UpdateStatus(ctx, account.ID, model.AccountStatusLoginRequired); updateErr != nil {
					s.logger.Error("mark account login required after listener auth failure", zap.Int64("account_id", account.ID), zap.Error(updateErr))
				}
			}
			s.logger.Error("telegram update listener auth failure", zap.Int64("account_id", account.ID), zap.Error(err))
			return
		}
		if classification.Kind == retry.KindPermanent {
			s.logger.Error("telegram update listener permanent failure", zap.Int64("account_id", account.ID), zap.Error(err))
			return
		}
		status := model.AccountStatusReconnecting
		if classification.Kind == retry.KindFloodWait {
			status = model.AccountStatusFloodWait
		}
		if s.accounts != nil {
			if updateErr := s.accounts.UpdateStatus(ctx, account.ID, status); updateErr != nil {
				s.logger.Error("mark account after listener failure", zap.Int64("account_id", account.ID), zap.String("status", status), zap.Error(updateErr))
			}
		}
		account.Status = status
		hadFailure = true
		delay := s.retryPolicy.Delay(1, classification)
		s.logger.Info("telegram update listener retry scheduled",
			zap.Int64("account_id", account.ID),
			zap.String("classification", string(classification.Kind)),
			zap.Duration("delay", delay),
		)
		sleep := s.retryPolicy.Sleep
		if sleep == nil {
			sleep = sleepContext
		}
		if sleepErr := sleep(ctx, delay); sleepErr != nil {
			return
		}
	}
}

func (s *Service) runListenerAttempt(ctx context.Context, account model.Account) (error, bool) {
	if account.Status != model.AccountStatusReconnecting || s.accounts == nil {
		return s.listener.Run(ctx, account, func(event Event) error {
			return s.Enqueue(ctx, event)
		}), false
	}

	errc := make(chan error, 1)
	go func() {
		errc <- s.listener.Run(ctx, account, func(event Event) error {
			return s.Enqueue(ctx, event)
		})
	}()

	timer := time.NewTimer(s.onlineDelay)
	defer timer.Stop()
	select {
	case err := <-errc:
		return err, false
	case <-timer.C:
		if updateErr := s.accounts.UpdateStatus(ctx, account.ID, model.AccountStatusOnline); updateErr != nil {
			s.logger.Error("mark account online after listener stabilizes", zap.Int64("account_id", account.ID), zap.Error(updateErr))
		}
		err := <-errc
		return err, true
	}
}

func (s *Service) hasListenEnabledChannels(ctx context.Context, accountID int64) (bool, error) {
	if s.channels == nil {
		return true, nil
	}
	channels, err := s.channels.FindByAccountID(ctx, accountID)
	if err != nil {
		return false, err
	}
	for _, channel := range channels {
		if channel.ListenEnabled {
			return true, nil
		}
	}
	return false, nil
}

func wait(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
