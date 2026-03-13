package erpnext

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (c *Client) SearchCompanies(ctx context.Context, baseURL, apiKey, apiSecret string, limit int) ([]Company, error) {
	normalized, err := normalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	endpoint := normalized + "/api/resource/Company?fields=%5B%22name%22%5D&limit_page_length=" + fmt.Sprintf("%d", limit)
	var payload struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := c.doJSON(ctx, endpoint, apiKey, apiSecret, &payload); err != nil {
		return nil, err
	}

	items := make([]Company, 0, len(payload.Data))
	for _, row := range payload.Data {
		items = append(items, Company{Name: strings.TrimSpace(row.Name)})
	}
	return items, nil
}

func (c *Client) CreateAndSubmitDeliveryNote(ctx context.Context, baseURL, apiKey, apiSecret string, input CreateDeliveryNoteInput) (DeliveryNoteResult, error) {
	normalized, err := normalizeBaseURL(baseURL)
	if err != nil {
		return DeliveryNoteResult{}, err
	}
	if input.Qty <= 0 {
		return DeliveryNoteResult{}, fmt.Errorf("qty must be greater than 0")
	}
	if strings.TrimSpace(input.Customer) == "" {
		return DeliveryNoteResult{}, fmt.Errorf("customer is required")
	}
	if strings.TrimSpace(input.Company) == "" {
		return DeliveryNoteResult{}, fmt.Errorf("company is required")
	}
	if strings.TrimSpace(input.Warehouse) == "" {
		return DeliveryNoteResult{}, fmt.Errorf("warehouse is required")
	}
	if strings.TrimSpace(input.ItemCode) == "" {
		return DeliveryNoteResult{}, fmt.Errorf("item code is required")
	}
	if strings.TrimSpace(input.UOM) == "" {
		input.UOM = "Nos"
	}

	payload := map[string]interface{}{
		"customer":      strings.TrimSpace(input.Customer),
		"company":       strings.TrimSpace(input.Company),
		"set_warehouse": strings.TrimSpace(input.Warehouse),
		"items": []map[string]interface{}{
			{
				"item_code":         strings.TrimSpace(input.ItemCode),
				"qty":               input.Qty,
				"uom":               strings.TrimSpace(input.UOM),
				"stock_uom":         strings.TrimSpace(input.UOM),
				"conversion_factor": 1,
				"warehouse":         strings.TrimSpace(input.Warehouse),
			},
		},
	}

	var createResp struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	createEndpoint := normalized + "/api/resource/Delivery%20Note"
	if err := c.doJSONRequest(ctx, http.MethodPost, createEndpoint, apiKey, apiSecret, payload, &createResp); err != nil {
		return DeliveryNoteResult{}, err
	}
	if createResp.Data.Name == "" {
		return DeliveryNoteResult{}, fmt.Errorf("delivery note create response did not return name")
	}

	submitPayload := map[string]interface{}{
		"doc": map[string]interface{}{},
	}
	submitEndpoint := normalized + "/api/method/frappe.client.submit"
	docEndpoint := normalized + "/api/resource/Delivery%20Note/" + url.PathEscape(createResp.Data.Name)
	for attempt := 0; attempt < 2; attempt++ {
		var latest struct {
			Data map[string]interface{} `json:"data"`
		}
		if err := c.doJSON(ctx, docEndpoint, apiKey, apiSecret, &latest); err != nil {
			return DeliveryNoteResult{}, err
		}
		if len(latest.Data) == 0 {
			return DeliveryNoteResult{}, fmt.Errorf("delivery note %s not found after create", createResp.Data.Name)
		}
		submitPayload["doc"] = latest.Data

		if err := c.doJSONRequest(ctx, http.MethodPost, submitEndpoint, apiKey, apiSecret, submitPayload, nil); err != nil {
			if attempt == 0 && strings.Contains(err.Error(), "TimestampMismatchError") {
				continue
			}
			return DeliveryNoteResult{}, err
		}
		break
	}

	return DeliveryNoteResult{Name: createResp.Data.Name}, nil
}
