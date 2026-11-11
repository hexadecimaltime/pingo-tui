package history

import (
	"context"
)

type Store interface {
	UpsertSession(ctx context.Context, code string) error
	SaveAnswer(ctx context.Context, sessionCode, question, answer string, options []string, questionType string) error
	ListSessions(ctx context.Context) ([]Session, error)
	UpdateSessionName(ctx context.Context, code, name string) error
	UpdateCorrectAnswer(ctx context.Context, sessionCode, correctAnswer string) error
	ListAnswers(ctx context.Context) ([]Answer, error)
	UpdateCorrectAnswerByID(ctx context.Context, id int64, correctAnswer string) error
}

type NoOpStore struct{}

func (n *NoOpStore) UpsertSession(ctx context.Context, code string) error { return nil }
func (n *NoOpStore) SaveAnswer(ctx context.Context, sessionCode, question, answer string, options []string, questionType string) error {
	return nil
}
func (n *NoOpStore) ListSessions(ctx context.Context) ([]Session, error)            { return []Session{}, nil }
func (n *NoOpStore) UpdateSessionName(ctx context.Context, code, name string) error { return nil }
func (n *NoOpStore) UpdateCorrectAnswer(ctx context.Context, sessionCode, correctAnswer string) error {
	return nil
}
func (n *NoOpStore) ListAnswers(ctx context.Context) ([]Answer, error) { return []Answer{}, nil }
func (n *NoOpStore) UpdateCorrectAnswerByID(ctx context.Context, id int64, correctAnswer string) error {
	return nil
}
