package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/yolo-labz/wa/v2/internal/app"
	"github.com/yolo-labz/wa/v2/internal/domain"
)

// adminPairer is the admin UI's pair method (spec 116 ruling D2): the
// daemon's own "pair" job, owned by the admin's TokenPrincipal, so
// jobs.cancel, owner revocation and shutdown reach it as for any caller.
// The job's request values carry the flow's QR sink to Adapter.Pair,
// which runs under the job ctx; ending ctx cancels the job.
type adminPairer struct {
	da      *dispatcherAdapter
	session app.SessionStore
}

func (p adminPairer) Paired(ctx context.Context) (bool, error) {
	s, err := p.session.Load(ctx)
	return !s.IsZero(), err
}

// pairDrain bounds the wait for a cancelled pair job's worker to exit.
const pairDrain = 10 * time.Second

func (p adminPairer) Pair(ctx context.Context, tokenID string) error {
	owner := app.TokenPrincipal(tokenID)
	raw, err := p.da.Handle(app.WithCaller(ctx, owner), "pair", json.RawMessage(`{"async":true}`))
	var job app.JobView
	if err == nil {
		err = json.Unmarshal(raw, &job)
	}
	// The daemon created the job, so it stops it even when the owner's
	// token is already revoked (Cancel would refuse), and Pair returns
	// only once the worker is gone: a later PairSuccess is not this flow's.
	stop := func() {
		select {
		case <-p.da.exec.CancelOwned(owner, job.ID):
		case <-time.After(pairDrain):
		}
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for err == nil {
		select {
		case <-ctx.Done():
			stop()
			return ctx.Err()
		case <-tick.C:
		}
		v, gerr := p.da.exec.Get(ctx, owner, job.ID)
		switch {
		case gerr != nil:
			err = gerr
			stop()
		case v.State == app.StateDone:
			return nil
		case v.State == app.StateFailed:
			err = v.Err
		case v.State == app.StateCancelled || v.State == app.StateExpired:
			err = errors.New("pair job " + v.State)
		}
	}
	return err
}

// watchPairSuccess calls onSuccess for each PairSuccess on events until
// ctx ends or events closes.
func watchPairSuccess(ctx context.Context, events <-chan app.Event, onSuccess func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-events:
			if !ok {
				return
			}
			if pe, ok := e.Payload.(domain.PairingEvent); ok && pe.State == domain.PairSuccess {
				onSuccess()
			}
		}
	}
}
