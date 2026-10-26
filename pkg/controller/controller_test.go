package controller

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"go-mod-select/pkg/updater"
)

func TestUpdateFailureNeverClaimsAFailedRollbackSucceeded(t *testing.T) {
	failedRollback := fmt.Errorf("go get: boom (%w)", fmt.Errorf("%w: restore go.mod: denied", updater.ErrRollbackFailed))

	got := updateFailure(failedRollback).Error()
	if strings.Contains(got, "restored)") {
		t.Errorf("message claims the files were restored: %s", got)
	}
	if !strings.Contains(got, "incomplete") {
		t.Errorf("message should say the rollback was incomplete: %s", got)
	}
	if !errors.Is(updateFailure(failedRollback), updater.ErrRollbackFailed) {
		t.Error("the cause should stay matchable")
	}
}

func TestUpdateFailureWording(t *testing.T) {
	if got := updateFailure(fmt.Errorf("x: %w", updater.ErrCancelled)).Error(); !strings.Contains(got, "interrupted") || !strings.Contains(got, "restored") {
		t.Errorf("cancelled run: %s", got)
	}
	if got := updateFailure(errors.New("go get: no such module")).Error(); !strings.Contains(got, "restored") || !strings.Contains(got, "no such module") {
		t.Errorf("ordinary failure: %s", got)
	}
}

func TestUpdateFailureForABrokenBuild(t *testing.T) {
	err := fmt.Errorf("%w: go build: exit status 1", updater.ErrVerifyFailed)

	got := updateFailure(err).Error()
	if !strings.Contains(got, "no longer builds") || !strings.Contains(got, "restored") {
		t.Errorf("message should say the build broke and the update was rolled back: %s", got)
	}
	if !errors.Is(updateFailure(err), updater.ErrVerifyFailed) {
		t.Error("the cause should stay matchable")
	}
}
