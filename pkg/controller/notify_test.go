/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/notifications"
)

type captureChannel struct {
	got chan *notifications.Notification
}

func (c *captureChannel) Name() string { return "capture" }
func (c *captureChannel) Send(_ context.Context, n *notifications.Notification) error {
	c.got <- n
	return nil
}

func TestReconciler_NotifyFansOutToOwner(t *testing.T) {
	cap := &captureChannel{got: make(chan *notifications.Notification, 1)}
	notifier := notifications.NewNotifier(slog.New(slog.NewTextHandler(io.Discard, nil)))
	notifier.RegisterChannel(cap)

	r := &SandboxReconciler{Notifier: notifier, NotifyChannels: []string{"capture"}}
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-1"},
		Spec: agenttierv1alpha1.SandboxSpec{
			CreatedBy: &agenttierv1alpha1.UserIdentity{Sub: "u-1", Email: "owner@example.com"},
		},
	}

	r.notify(context.Background(), sandbox, notifications.NotifyError, "it broke")

	select {
	case n := <-cap.got:
		if n.Type != notifications.NotifyError || n.Message != "it broke" {
			t.Errorf("unexpected notification %+v", n)
		}
		if n.UserEmail != "owner@example.com" || n.SandboxName != "sb-1" {
			t.Errorf("expected owner email + sandbox name, got email=%q name=%q", n.UserEmail, n.SandboxName)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("notify did not deliver to the channel")
	}
}

func TestReconciler_NotifyNoopWhenUnconfigured(t *testing.T) {
	// No Notifier / no channels → must not panic.
	r := &SandboxReconciler{}
	r.notify(context.Background(), &agenttierv1alpha1.Sandbox{}, notifications.NotifyError, "x")
}
