package testkit

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
)

// RecordingPusher заменяет сгенерённый мок: очередь принимает *sql.Tx, а его
// поля дописывает фоновая горутина database/sql, и рефлексия testify
// прочитала бы их гонкой при разборе аргументов.
type RecordingPusher struct {
	Transactions []*sql.Tx
	Numbers      []string
	Err          error
}

func (p *RecordingPusher) Push(_ context.Context, tx *sql.Tx, number string) error {
	p.Transactions = append(p.Transactions, tx)
	p.Numbers = append(p.Numbers, number)

	return p.Err
}

// ExpectPushes требует к концу теста ровно столько принятых заданий.
func (p *RecordingPusher) ExpectPushes(t testing.TB, calls int) *RecordingPusher {
	t.Helper()
	t.Cleanup(func() {
		assert.Len(t, p.Numbers, calls, "очередь получила не столько заданий")
	})

	return p
}
