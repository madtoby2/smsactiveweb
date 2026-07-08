package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"sms-platform/internal/store"
)

func (s *Server) publicProducts(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListProducts(true)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	jsonOut(w, http.StatusOK, items)
}

func (s *Server) purchaseProduct(w http.ResponseWriter, r *http.Request, u store.User) {
	var in struct {
		ProductCode string `json:"productCode"`
		PayType     int    `json:"payType"`
	}
	if decode(r, &in) != nil || strings.TrimSpace(in.ProductCode) == "" {
		fail(w, http.StatusBadRequest, "请选择商品")
		return
	}
	product, err := s.Store.GetProduct(in.ProductCode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, http.StatusNotFound, "商品不存在")
			return
		}
		fail(w, http.StatusInternalServerError, err)
		return
	}
	if !product.Active {
		fail(w, http.StatusConflict, "商品已下架")
		return
	}
	if product.AvailableCount <= 0 {
		fail(w, http.StatusConflict, "商品暂时无库存")
		return
	}
	if in.PayType == 0 {
		in.PayType = 2
	}
	if in.PayType != 1 && in.PayType != 2 && in.PayType != 3 && in.PayType != 11 {
		fail(w, http.StatusBadRequest, "unsupported payment method")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	order := store.ProductOrder{
		ID:          store.ID("T"),
		UserID:      u.ID,
		ProductCode: product.Code,
		ProductName: product.Name,
		PriceFen:    product.PriceFen,
		CreatedAt:   now,
	}
	raw, _ := store.Token()
	payment := store.Recharge{ID: store.ID("P"), UserID: u.ID, AmountFen: order.PriceFen, Provider: s.C.PayProvider, PayType: strconv.Itoa(in.PayType), Token: raw, Reference: order.ID, CreatedAt: now}
	if err = s.Store.CreateProductPayment(u, order, payment); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	if s.C.PayProvider == "sandbox" {
		jsonOut(w, http.StatusCreated, map[string]any{"id": order.ID, "paymentId": payment.ID, "priceFen": order.PriceFen, "checkoutUrl": fmt.Sprintf("/sandbox/pay/%s?token=%s", payment.ID, url.QueryEscape(raw))})
		return
	}
	if s.C.PayProvider == "epay" || s.C.PayProvider == "50pay" {
		checkoutURL, err := s.EPay.CheckoutURL(payment.ID, order.PriceFen, in.PayType, s.C.BaseURL+"/api/payments/epay/notify", s.C.BaseURL+"/api/payments/epay/return")
		if err != nil {
			_ = s.Store.SetRechargeStatus(payment.ID, "failed")
			fail(w, http.StatusBadGateway, err)
			return
		}
		jsonOut(w, http.StatusCreated, map[string]any{"id": order.ID, "paymentId": payment.ID, "priceFen": order.PriceFen, "checkoutUrl": checkoutURL})
		return
	}
	fail(w, http.StatusServiceUnavailable, "payment provider is not configured")
}

func (s *Server) productOrders(w http.ResponseWriter, r *http.Request, u store.User) {
	items, err := s.Store.ListProductOrders(u.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	jsonOut(w, http.StatusOK, items)
}

func (s *Server) productOrderStatus(w http.ResponseWriter, r *http.Request, u store.User) {
	item, err := s.Store.GetProductOrder(r.PathValue("id"), u.ID)
	if err != nil {
		fail(w, http.StatusNotFound, "订单不存在")
		return
	}
	jsonOut(w, http.StatusOK, item)
}

func (s *Server) productOrderCheckout(w http.ResponseWriter, r *http.Request, u store.User) {
	order, err := s.Store.GetProductOrder(r.PathValue("id"), u.ID)
	if err != nil {
		fail(w, http.StatusNotFound, "订单不存在")
		return
	}
	if order.Status != "awaiting_payment" {
		fail(w, http.StatusConflict, "order is not awaiting payment")
		return
	}
	recharge, err := s.Store.GetRechargeByReference(order.ID)
	if err != nil {
		fail(w, http.StatusNotFound, "payment order not found")
		return
	}
	if recharge.Status != "pending" {
		fail(w, http.StatusConflict, "payment is no longer pending")
		return
	}
	if s.C.PayProvider == "sandbox" {
		jsonOut(w, http.StatusOK, map[string]any{"checkoutUrl": fmt.Sprintf("/sandbox/pay/%s?token=%s", recharge.ID, url.QueryEscape(recharge.Token))})
		return
	}
	if s.C.PayProvider == "epay" || s.C.PayProvider == "50pay" {
		payType, convErr := strconv.Atoi(strings.TrimSpace(recharge.PayType))
		if convErr != nil {
			fail(w, http.StatusInternalServerError, convErr)
			return
		}
		checkoutURL, checkoutErr := s.EPay.CheckoutURL(recharge.ID, recharge.AmountFen, payType, s.C.BaseURL+"/api/payments/epay/notify", s.C.BaseURL+"/api/payments/epay/return")
		if checkoutErr != nil {
			fail(w, http.StatusBadGateway, checkoutErr)
			return
		}
		jsonOut(w, http.StatusOK, map[string]any{"checkoutUrl": checkoutURL})
		return
	}
	fail(w, http.StatusConflict, "continue payment is unavailable for this provider")
}

func (s *Server) runPaidProductOrderBatch(ctx context.Context) {
	orders, err := s.Store.ListPaidProductOrders(20)
	if err != nil {
		log.Printf("paid product order scan failed: %v", err)
		return
	}
	for _, item := range orders {
		s.fulfillPaidProductOrder(ctx, item)
	}
}

func (s *Server) fulfillPaidProductOrder(_ context.Context, order store.ProductOrder) {
	claimed, err := s.Store.ClaimPaidProductOrder(order.ID)
	if err != nil || !claimed {
		return
	}
	if _, err = s.Store.ReserveProductCredential(order.ID, order.ProductCode); err != nil {
		_ = s.Store.ReleaseProductOrder(order.ID)
		log.Printf("paid product order %s is waiting for inventory: %v", order.ID, err)
		return
	}
}

func (s *Server) adminProducts(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListProducts(false)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	jsonOut(w, http.StatusOK, items)
}

func (s *Server) adminSaveProduct(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		Category    string `json:"category"`
		Description string `json:"description"`
		PriceFen    int64  `json:"priceFen"`
		Active      bool   `json:"active"`
	}
	if decode(r, &in) != nil {
		fail(w, http.StatusBadRequest, "invalid request")
		return
	}
	if pathCode := strings.TrimSpace(r.PathValue("code")); pathCode != "" {
		in.Code = pathCode
	}
	item := store.Product{
		Code:        strings.TrimSpace(in.Code),
		Name:        strings.TrimSpace(in.Name),
		Category:    strings.TrimSpace(in.Category),
		Description: strings.TrimSpace(in.Description),
		PriceFen:    in.PriceFen,
		Active:      in.Active,
	}
	if err := s.Store.UpsertProduct(item); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	created, err := s.Store.GetProduct(item.Code)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	jsonOut(w, http.StatusOK, created)
}

func (s *Server) adminProductInventory(w http.ResponseWriter, r *http.Request) {
	items, err := s.Store.ListProductInventory(r.PathValue("code"))
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	jsonOut(w, http.StatusOK, items)
}

func (s *Server) adminAddProductInventory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Credentials []string `json:"credentials"`
		Credential  string   `json:"credential"`
	}
	if decode(r, &in) != nil {
		fail(w, http.StatusBadRequest, "invalid request")
		return
	}
	code := r.PathValue("code")
	entries := make([]string, 0, len(in.Credentials)+1)
	for _, item := range in.Credentials {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			entries = append(entries, trimmed)
		}
	}
	if trimmed := strings.TrimSpace(in.Credential); trimmed != "" {
		entries = append(entries, trimmed)
	}
	if len(entries) == 0 {
		fail(w, http.StatusBadRequest, "请提供凭证")
		return
	}
	for _, credential := range entries {
		if err := s.Store.AddProductInventory(code, credential); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
	}
	items, err := s.Store.ListProductInventory(code)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	jsonOut(w, http.StatusCreated, items)
}
