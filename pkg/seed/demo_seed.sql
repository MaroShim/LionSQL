-- ============================================================================
-- LionSQL E-Commerce & Banking Sample Database (demo_seed.sql)
-- Tables: customers, accounts, products, sales, sales_details
-- Stored Procedures: TransferFunds, ProcessOrder
-- ============================================================================

-- 1. Customers Table (고객 인적사항)
CREATE TABLE customers (
    id INT PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    email VARCHAR(100) NOT NULL,
    phone VARCHAR(50),
    city VARCHAR(50),
    created_at VARCHAR(30)
);

INSERT INTO customers (id, name, email, phone, city, created_at) VALUES
(1, 'Alice Kim', 'alice@lionsql.local', '010-1111-2222', 'Seoul', '2026-01-15 10:00:00'),
(2, 'Bob Park', 'bob@lionsql.local', '010-3333-4444', 'Busan', '2026-02-01 11:30:00'),
(3, 'Charlie Lee', 'charlie@lionsql.local', '010-5555-6666', 'Incheon', '2026-02-15 14:20:00'),
(4, 'Diana Choi', 'diana@lionsql.local', '010-7777-8888', 'Daegu', '2026-03-01 09:15:00'),
(5, 'Evan Jung', 'evan@lionsql.local', '010-9999-0000', 'Daejeon', '2026-03-10 16:45:00');

-- 2. Accounts Table (고객 계좌 및 잔액)
CREATE TABLE accounts (
    id INT PRIMARY KEY,
    customer_id INT NOT NULL,
    account_number VARCHAR(50) NOT NULL,
    balance DECIMAL(15, 2) NOT NULL,
    account_type VARCHAR(20) NOT NULL,
    status VARCHAR(20) NOT NULL,
    created_at VARCHAR(30)
);

INSERT INTO accounts (id, customer_id, account_number, balance, account_type, status, created_at) VALUES
(1, 1, 'ACC-1001', 5000000.00, 'checking', 'active', '2026-01-15 10:05:00'),
(2, 2, 'ACC-1002', 3200000.50, 'checking', 'active', '2026-02-01 11:35:00'),
(3, 3, 'ACC-1003', 1500000.00, 'savings', 'active', '2026-02-15 14:25:00'),
(4, 4, 'ACC-1004', 8900000.75, 'checking', 'active', '2026-03-01 09:20:00'),
(5, 5, 'ACC-1005', 450000.00, 'checking', 'dormant', '2026-03-10 16:50:00');

-- 3. Products Table (판매 상품 카탈로그)
CREATE TABLE products (
    id INT PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    category VARCHAR(50) NOT NULL,
    price DECIMAL(10, 2) NOT NULL,
    stock_qty INT NOT NULL,
    created_at VARCHAR(30)
);

INSERT INTO products (id, name, category, price, stock_qty, created_at) VALUES
(1, 'UltraBook Pro 15', 'Electronics', 1890000.00, 25, '2026-01-01 00:00:00'),
(2, 'Wireless Noise-Canceling Headphones', 'Electronics', 350000.00, 50, '2026-01-01 00:00:00'),
(3, 'Mechanical Gaming Keyboard', 'Electronics', 150000.00, 80, '2026-01-01 00:00:00'),
(4, 'High-Performance Database Internals', 'Books', 45000.00, 120, '2026-01-01 00:00:00'),
(5, 'Designing Data-Intensive Applications', 'Books', 42000.00, 100, '2026-01-01 00:00:00'),
(6, 'Ergonomic Office Chair', 'Furniture', 480000.00, 15, '2026-01-01 00:00:00'),
(7, 'Motorized Standing Desk', 'Furniture', 650000.00, 10, '2026-01-01 00:00:00'),
(8, '4K UHD 27-inch Monitor', 'Electronics', 520000.00, 30, '2026-01-01 00:00:00');

-- 4. Sales Table (주문 마스터)
CREATE TABLE sales (
    id INT PRIMARY KEY,
    customer_id INT NOT NULL,
    order_date VARCHAR(30) NOT NULL,
    total_amount DECIMAL(15, 2) NOT NULL,
    status VARCHAR(20) NOT NULL
);

INSERT INTO sales (id, customer_id, order_date, total_amount, status) VALUES
(1, 1, '2026-03-01 14:10:00', 2240000.00, 'COMPLETED'),
(2, 2, '2026-03-02 16:30:00', 87000.00, 'COMPLETED'),
(3, 3, '2026-03-05 10:20:00', 1170000.00, 'COMPLETED'),
(4, 1, '2026-03-08 19:45:00', 480000.00, 'COMPLETED'),
(5, 4, '2026-03-12 11:00:00', 150000.00, 'PENDING'),
(6, 5, '2026-03-14 15:25:00', 45000.00, 'CANCELLED');

-- 5. Sales Details Table (주문 상세 항목)
CREATE TABLE sales_details (
    id INT PRIMARY KEY,
    sales_id INT NOT NULL,
    product_id INT NOT NULL,
    quantity INT NOT NULL,
    unit_price DECIMAL(10, 2) NOT NULL,
    subtotal DECIMAL(15, 2) NOT NULL
);

INSERT INTO sales_details (id, sales_id, product_id, quantity, unit_price, subtotal) VALUES
(1, 1, 1, 1, 1890000.00, 1890000.00),
(2, 1, 2, 1, 350000.00, 350000.00),
(3, 2, 4, 1, 45000.00, 45000.00),
(4, 2, 5, 1, 42000.00, 42000.00),
(5, 3, 6, 1, 480000.00, 480000.00),
(6, 3, 7, 1, 650000.00, 650000.00),
(7, 3, 3, 1, 150000.00, 150000.00),
(8, 4, 6, 1, 480000.00, 480000.00),
(9, 5, 3, 1, 150000.00, 150000.00),
(10, 6, 4, 1, 45000.00, 45000.00);

-- 6. Sample Stored Procedure 1: TransferFunds (계좌 이체)
CREATE PROCEDURE TransferFunds
    @FromAccount INT,
    @ToAccount INT,
    @Amount DECIMAL(15, 2)
AS
BEGIN
    BEGIN TRANSACTION;
    DECLARE @FromBal DECIMAL(15, 2);
    SELECT @FromBal = balance FROM accounts WHERE id = @FromAccount;
    IF @FromBal < @Amount
    BEGIN
        ROLLBACK TRANSACTION;
        THROW 50001, 'Insufficient funds in source account', 1;
        RETURN;
    END;
    UPDATE accounts SET balance = balance - @Amount WHERE id = @FromAccount;
    UPDATE accounts SET balance = balance + @Amount WHERE id = @ToAccount;
    COMMIT TRANSACTION;
    SELECT 'Transfer successful' AS result, @Amount AS transferred_amount;
END;

-- 7. Sample Stored Procedure 2: ProcessOrder (신규 주문 등록 및 재고 차감)
CREATE PROCEDURE ProcessOrder
    @SaleId INT,
    @CustomerId INT,
    @ProductId INT,
    @Quantity INT
AS
BEGIN
    BEGIN TRANSACTION;
    DECLARE @Stock INT;
    DECLARE @Price DECIMAL(10, 2);
    SELECT @Stock = stock_qty, @Price = price FROM products WHERE id = @ProductId;
    IF @Stock < @Quantity
    BEGIN
        ROLLBACK TRANSACTION;
        THROW 50002, 'Insufficient stock for product', 1;
        RETURN;
    END;
    UPDATE products SET stock_qty = stock_qty - @Quantity WHERE id = @ProductId;
    DECLARE @Total DECIMAL(15, 2);
    SET @Total = @Price * @Quantity;
    INSERT INTO sales (id, customer_id, order_date, total_amount, status) VALUES (@SaleId, @CustomerId, '2026-03-15 12:00:00', @Total, 'COMPLETED');
    INSERT INTO sales_details (id, sales_id, product_id, quantity, unit_price, subtotal) VALUES (@SaleId * 10, @SaleId, @ProductId, @Quantity, @Price, @Total);
    COMMIT TRANSACTION;
    SELECT 'Order processed successfully' AS result, @Total AS total_amount;
END;
