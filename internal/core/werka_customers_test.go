package core

import (
	"context"
	"testing"

	"mobile_server/internal/erpnext"
)

func TestWerkaCustomersOnlyReturnsCustomersWithAssignedItems(t *testing.T) {
	stub := &adminSuppliersERPStub{
		searchCustomers: func(ctx context.Context, baseURL, apiKey, apiSecret, query string, limit int) ([]erpnext.Customer, error) {
			return []erpnext.Customer{
				{ID: "CUS-001", Name: "Vali", Phone: "+998901111111"},
				{ID: "CUS-002", Name: "Ali", Phone: "+998902222222"},
				{ID: "CUS-003", Name: "Sami", Phone: "+998903333333"},
			}, nil
		},
		listCustomerItems: func(ctx context.Context, baseURL, apiKey, apiSecret, customerRef, query string, limit int) ([]erpnext.Item, error) {
			switch customerRef {
			case "CUS-001":
				return []erpnext.Item{{Code: "ITEM-001", Name: "Un"}}, nil
			case "CUS-002":
				return nil, nil
			case "CUS-003":
				return []erpnext.Item{{Code: "ITEM-003", Name: "Shakar"}}, nil
			default:
				return nil, nil
			}
		},
	}

	auth := NewERPAuthenticator(
		stub,
		"http://erp.test",
		"key",
		"secret",
		"Stores - A",
		"10",
		"20",
		"",
		"",
		"",
		nil,
		nil,
	)

	items, err := auth.WerkaCustomers(context.Background(), "", 200)
	if err != nil {
		t.Fatalf("WerkaCustomers() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 active customers, got %d", len(items))
	}
	if items[0].Ref != "CUS-001" || items[1].Ref != "CUS-003" {
		t.Fatalf("unexpected customer refs: %+v", items)
	}
}

func TestWerkaCustomerItemOptionsReturnsAssignedPairs(t *testing.T) {
	stub := &adminSuppliersERPStub{
		searchCustomers: func(ctx context.Context, baseURL, apiKey, apiSecret, query string, limit int) ([]erpnext.Customer, error) {
			return []erpnext.Customer{
				{ID: "CUS-001", Name: "Vali", Phone: "+998901111111"},
				{ID: "CUS-002", Name: "Ali", Phone: "+998902222222"},
			}, nil
		},
		searchItems: func(ctx context.Context, baseURL, apiKey, apiSecret, query string, limit int) ([]erpnext.Item, error) {
			return []erpnext.Item{
				{Code: "ITEM-001", Name: "Un", UOM: "Kg"},
				{Code: "ITEM-002", Name: "Shakar", UOM: "Kg"},
			}, nil
		},
		getItemCustomerAssignment: func(ctx context.Context, baseURL, apiKey, apiSecret, itemCode string) (erpnext.ItemCustomerAssignment, error) {
			switch itemCode {
			case "ITEM-001":
				return erpnext.ItemCustomerAssignment{
					Code:         itemCode,
					CustomerRefs: []string{"CUS-001", "CUS-002"},
				}, nil
			case "ITEM-002":
				return erpnext.ItemCustomerAssignment{
					Code:         itemCode,
					CustomerRefs: []string{"CUS-002"},
				}, nil
			default:
				return erpnext.ItemCustomerAssignment{Code: itemCode}, nil
			}
		},
	}

	auth := NewERPAuthenticator(
		stub,
		"http://erp.test",
		"key",
		"secret",
		"Stores - A",
		"10",
		"20",
		"",
		"",
		"",
		nil,
		nil,
	)

	items, err := auth.WerkaCustomerItemOptions(context.Background(), "", 200)
	if err != nil {
		t.Fatalf("WerkaCustomerItemOptions() error = %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 assigned customer-item pairs, got %d", len(items))
	}
}
