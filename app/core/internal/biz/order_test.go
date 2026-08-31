package biz

import "testing"

func TestOrderClosedSetsAndTransitions(t *testing.T) {
	orderType, err := ParseOrderType("meeting_quota")
	if err != nil || orderType != OrderTypeMeetingQuota {
		t.Fatalf("ParseOrderType() = (%v, %v)", orderType, err)
	}
	if _, err := ParseOrderType("unknown"); err == nil {
		t.Fatal("ParseOrderType(unknown) expected error")
	}
	for _, status := range []OrderStatus{OrderStatusPending, OrderStatusPaid, OrderStatusCancelled, OrderStatusRefunded} {
		parsed, parseErr := ParseOrderStatus(status.String())
		if parseErr != nil || parsed != status {
			t.Fatalf("ParseOrderStatus(%q) = (%v, %v)", status.String(), parsed, parseErr)
		}
	}
	if !OrderStatusPending.CanTransitionTo(OrderStatusPaid) ||
		!OrderStatusPending.CanTransitionTo(OrderStatusCancelled) ||
		!OrderStatusPaid.CanTransitionTo(OrderStatusRefunded) ||
		OrderStatusPaid.CanTransitionTo(OrderStatusCancelled) ||
		OrderStatusRefunded.CanTransitionTo(OrderStatusPaid) {
		t.Fatal("order status transition table is invalid")
	}
}
