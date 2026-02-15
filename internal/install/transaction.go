package install

import (
	"errors"
	"fmt"
	"strings"
)

type Step struct {
	Name string
	Do   func() error
	Undo func() error
}

type Transaction struct {
	steps  []Step
	onStep func(step string) error
}

type RollbackError struct {
	Cause        error
	RollbackErrs []error
}

func (e *RollbackError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("transaction failed: %v", e.Cause)}
	for _, rb := range e.RollbackErrs {
		parts = append(parts, fmt.Sprintf("rollback error: %v", rb))
	}
	return strings.Join(parts, "; ")
}

func (e *RollbackError) Unwrap() error {
	if e == nil {
		return nil
	}
	if len(e.RollbackErrs) == 0 {
		return e.Cause
	}
	joined := append([]error{e.Cause}, e.RollbackErrs...)
	return errors.Join(joined...)
}

func New(onStep func(step string) error) *Transaction {
	return &Transaction{onStep: onStep}
}

func (t *Transaction) Add(name string, do func() error, undo func() error) {
	t.steps = append(t.steps, Step{Name: name, Do: do, Undo: undo})
}

func (t *Transaction) Run() error {
	executed := make([]Step, 0, len(t.steps))
	for _, step := range t.steps {
		if t.onStep != nil {
			if err := t.onStep(step.Name); err != nil {
				return err
			}
		}
		if step.Do != nil {
			if err := step.Do(); err != nil {
				rbErrs := rollback(executed)
				if len(rbErrs) > 0 {
					return &RollbackError{Cause: err, RollbackErrs: rbErrs}
				}
				return err
			}
		}
		executed = append(executed, step)
	}
	return nil
}

func rollback(executed []Step) []error {
	var rbErrs []error
	for i := len(executed) - 1; i >= 0; i-- {
		undo := executed[i].Undo
		if undo == nil {
			continue
		}
		if err := undo(); err != nil {
			rbErrs = append(rbErrs, fmt.Errorf("step %s: %w", executed[i].Name, err))
		}
	}
	return rbErrs
}
