package biz

import "fmt"

// OrderType identifies the product represented by an order.
type OrderType uint8

const (
	OrderTypeUnspecified OrderType = iota
	OrderTypeMeetingQuota
)

func (t OrderType) String() string {
	if t == OrderTypeMeetingQuota {
		return "meeting_quota"
	}
	return ""
}

func ParseOrderType(raw string) (OrderType, error) {
	if raw == OrderTypeMeetingQuota.String() {
		return OrderTypeMeetingQuota, nil
	}
	return OrderTypeUnspecified, fmt.Errorf("unknown order type %q", raw)
}

// OrderStatus is the payment lifecycle of an order.
type OrderStatus uint8

const (
	OrderStatusUnspecified OrderStatus = iota
	OrderStatusPending
	OrderStatusPaid
	OrderStatusCancelled
	OrderStatusRefunded
)

func (s OrderStatus) String() string {
	switch s {
	case OrderStatusPending:
		return "pending"
	case OrderStatusPaid:
		return "paid"
	case OrderStatusCancelled:
		return "cancelled"
	case OrderStatusRefunded:
		return "refunded"
	default:
		return ""
	}
}

func ParseOrderStatus(raw string) (OrderStatus, error) {
	switch raw {
	case OrderStatusPending.String():
		return OrderStatusPending, nil
	case OrderStatusPaid.String():
		return OrderStatusPaid, nil
	case OrderStatusCancelled.String():
		return OrderStatusCancelled, nil
	case OrderStatusRefunded.String():
		return OrderStatusRefunded, nil
	default:
		return OrderStatusUnspecified, fmt.Errorf("unknown order status %q", raw)
	}
}

func (s OrderStatus) CanTransitionTo(next OrderStatus) bool {
	switch s {
	case OrderStatusPending:
		return next == OrderStatusPaid || next == OrderStatusCancelled
	case OrderStatusPaid:
		return next == OrderStatusRefunded
	default:
		return false
	}
}
