package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *Store) ListProducts(activeOnly bool) ([]Product, error) {
	query := `
		SELECT p.code,p.name,p.category,p.description,p.price_fen,p.active,p.created_at,p.updated_at,
			COALESCE((SELECT COUNT(*) FROM product_inventory i WHERE i.product_code=p.code AND i.status='available'),0)
		FROM product_catalog p`
	args := []any{}
	if activeOnly {
		query += ` WHERE p.active=1`
	}
	query += ` ORDER BY p.updated_at DESC, p.code`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Product
	for rows.Next() {
		var item Product
		if err = rows.Scan(&item.Code, &item.Name, &item.Category, &item.Description, &item.PriceFen, &item.Active, &item.CreatedAt, &item.UpdatedAt, &item.AvailableCount); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetProduct(code string) (Product, error) {
	var item Product
	err := s.DB.QueryRow(`
		SELECT p.code,p.name,p.category,p.description,p.price_fen,p.active,p.created_at,p.updated_at,
			COALESCE((SELECT COUNT(*) FROM product_inventory i WHERE i.product_code=p.code AND i.status='available'),0)
		FROM product_catalog p
		WHERE p.code=?`, strings.TrimSpace(code)).
		Scan(&item.Code, &item.Name, &item.Category, &item.Description, &item.PriceFen, &item.Active, &item.CreatedAt, &item.UpdatedAt, &item.AvailableCount)
	return item, err
}

func (s *Store) UpsertProduct(item Product) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(item.Code) == "" || strings.TrimSpace(item.Name) == "" {
		return errors.New("product code and name are required")
	}
	if item.Category == "" {
		item.Category = "telegram_account"
	}
	if item.PriceFen <= 0 {
		return errors.New("product price must be positive")
	}
	_, err := s.DB.Exec(`
		INSERT INTO product_catalog(code,name,category,description,price_fen,active,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(code) DO UPDATE SET
			name=excluded.name,
			category=excluded.category,
			description=excluded.description,
			price_fen=excluded.price_fen,
			active=excluded.active,
			updated_at=excluded.updated_at
	`, strings.TrimSpace(item.Code), strings.TrimSpace(item.Name), strings.TrimSpace(item.Category), strings.TrimSpace(item.Description), item.PriceFen, item.Active, now, now)
	return err
}

func (s *Store) AddProductInventory(productCode, credential string) error {
	productCode = strings.TrimSpace(productCode)
	credential = strings.TrimSpace(credential)
	if productCode == "" || credential == "" {
		return errors.New("product code and credential are required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(`INSERT INTO product_inventory(product_code,credential,status,order_id,created_at,updated_at) VALUES(?,?,'available','',?,?)`, productCode, credential, now, now)
	return err
}

func (s *Store) ListProductInventory(productCode string) ([]ProductInventoryItem, error) {
	rows, err := s.DB.Query(`SELECT id,product_code,credential,status,order_id,created_at,updated_at FROM product_inventory WHERE product_code=? ORDER BY updated_at DESC,id DESC`, strings.TrimSpace(productCode))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductInventoryItem
	for rows.Next() {
		var item ProductInventoryItem
		if err = rows.Scan(&item.ID, &item.ProductCode, &item.Credential, &item.Status, &item.OrderID, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) CreateProductPayment(u User, order ProductOrder, payment Recharge) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO product_orders(id,user_id,product_code,product_name,credential,status,price_fen,refunded,created_at,delivered_at) VALUES(?,?,?,?,?,'awaiting_payment',?,0,?, '')`,
		order.ID, u.ID, order.ProductCode, order.ProductName, "", order.PriceFen, order.CreatedAt); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO recharges(id,user_id,amount_fen,provider,pay_type,status,provider_id,refund_provider_id,refunded_at,token,reference,created_at)VALUES(?,?,?,?,?,'pending','','','',?,?,?)`,
		payment.ID, u.ID, payment.AmountFen, payment.Provider, payment.PayType, payment.Token, order.ID, payment.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ClaimPaidProductOrder(id string) (bool, error) {
	result, err := s.DB.Exec(`UPDATE product_orders SET status='fulfilling' WHERE id=? AND status='paid'`, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *Store) ListPaidProductOrders(limit int) ([]ProductOrder, error) {
	rows, err := s.DB.Query(`SELECT id,user_id,product_code,product_name,credential,status,price_fen,refunded,created_at,delivered_at FROM product_orders WHERE status='paid' ORDER BY created_at LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductOrder
	for rows.Next() {
		var item ProductOrder
		if err = rows.Scan(&item.ID, &item.UserID, &item.ProductCode, &item.ProductName, &item.Credential, &item.Status, &item.PriceFen, &item.Refunded, &item.CreatedAt, &item.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ReserveProductCredential(orderID, productCode string) (ProductInventoryItem, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return ProductInventoryItem{}, err
	}
	defer tx.Rollback()
	var item ProductInventoryItem
	err = tx.QueryRow(`SELECT id,product_code,credential,status,order_id,created_at,updated_at FROM product_inventory WHERE product_code=? AND status='available' ORDER BY id LIMIT 1`, productCode).
		Scan(&item.ID, &item.ProductCode, &item.Credential, &item.Status, &item.OrderID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ProductInventoryItem{}, errors.New("no inventory available")
		}
		return ProductInventoryItem{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err = tx.Exec(`UPDATE product_inventory SET status='sold',order_id=?,updated_at=? WHERE id=? AND status='available'`, orderID, now, item.ID); err != nil {
		return ProductInventoryItem{}, err
	}
	if _, err = tx.Exec(`UPDATE product_orders SET credential=?,status='delivered',delivered_at=? WHERE id=? AND status='fulfilling'`, item.Credential, now, orderID); err != nil {
		return ProductInventoryItem{}, err
	}
	item.Status = "sold"
	item.OrderID = orderID
	item.UpdatedAt = now
	return item, tx.Commit()
}

func (s *Store) ReleaseProductOrder(id string) error {
	_, err := s.DB.Exec(`UPDATE product_orders SET status='paid' WHERE id=? AND status='fulfilling'`, id)
	return err
}

func (s *Store) ListProductOrders(uid int64) ([]ProductOrder, error) {
	rows, err := s.DB.Query(`SELECT id,user_id,product_code,product_name,credential,status,price_fen,refunded,created_at,delivered_at FROM product_orders WHERE user_id=? ORDER BY created_at DESC LIMIT 100`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductOrder
	for rows.Next() {
		var item ProductOrder
		if err = rows.Scan(&item.ID, &item.UserID, &item.ProductCode, &item.ProductName, &item.Credential, &item.Status, &item.PriceFen, &item.Refunded, &item.CreatedAt, &item.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetProductOrder(id string, uid int64) (ProductOrder, error) {
	var item ProductOrder
	err := s.DB.QueryRow(`SELECT id,user_id,product_code,product_name,credential,status,price_fen,refunded,created_at,delivered_at FROM product_orders WHERE id=? AND user_id=?`, id, uid).
		Scan(&item.ID, &item.UserID, &item.ProductCode, &item.ProductName, &item.Credential, &item.Status, &item.PriceFen, &item.Refunded, &item.CreatedAt, &item.DeliveredAt)
	return item, err
}

func (s *Store) GetProductOrderByID(id string) (ProductOrder, error) {
	var item ProductOrder
	err := s.DB.QueryRow(`SELECT id,user_id,product_code,product_name,credential,status,price_fen,refunded,created_at,delivered_at FROM product_orders WHERE id=?`, id).
		Scan(&item.ID, &item.UserID, &item.ProductCode, &item.ProductName, &item.Credential, &item.Status, &item.PriceFen, &item.Refunded, &item.CreatedAt, &item.DeliveredAt)
	return item, err
}

func (s *Store) CompleteProductPayment(id string, providerID string) (string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var reference, status, existingProviderID string
	if err = tx.QueryRow("SELECT reference,status,COALESCE(provider_id,'') FROM recharges WHERE id=?", id).Scan(&reference, &status, &existingProviderID); err != nil {
		return "", err
	}
	var orderExists int
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM product_orders WHERE id=?)", reference).Scan(&orderExists); err != nil {
		return "", err
	}
	if orderExists != 1 {
		return "", errors.New("payment is not linked to a product order")
	}
	if status == "paid" {
		if providerID != "" && existingProviderID != "" && providerID != existingProviderID {
			return "", errors.New("provider transaction mismatch")
		}
		return reference, tx.Commit()
	}
	if status != "pending" {
		return "", errors.New("payment is not pending")
	}
	if _, err = tx.Exec("UPDATE recharges SET status='paid',provider_id=CASE WHEN ?='' THEN provider_id ELSE ? END WHERE id=? AND status='pending'", providerID, providerID, id); err != nil {
		return "", err
	}
	if _, err = tx.Exec("UPDATE product_orders SET status='paid' WHERE id=? AND status='awaiting_payment'", reference); err != nil {
		return "", err
	}
	return reference, tx.Commit()
}
