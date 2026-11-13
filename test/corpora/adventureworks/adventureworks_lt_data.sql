-- ============================================================================
-- Microsoft AdventureWorksLT Sample Data Inserts
-- Canonical T-SQL Seed Data for LionSQL Compatibility Verification
-- ============================================================================

-- 1. ErrorLog Seed
INSERT INTO [dbo].[ErrorLog] ([ErrorTime], [UserName], [ErrorNumber], [ErrorSeverity], [ErrorState], [ErrorProcedure], [ErrorLine], [ErrorMessage])
VALUES ('2026-01-01 10:00:00', 'sa', 50000, 16, 1, 'usp_SampleProc', 42, 'Initial audit log bootstrap check.');

-- 2. Address Seed (5 Addresses)
INSERT INTO [SalesLT].[Address] ([AddressLine1], [AddressLine2], [City], [StateProvince], [CountryRegion], [PostalCode])
VALUES 
('1010 Clear Lake Blvd', NULL, 'Renton', 'Washington', 'United States', '98055'),
('2020 4th Ave', 'Suite 500', 'Seattle', 'Washington', 'United States', '98101'),
('3030 Olympic Dr', NULL, 'Bellevue', 'Washington', 'United States', '98004'),
('4040 Broadway Ave', '#12B', 'Portland', 'Oregon', 'United States', '97201'),
('5050 Oxford St', NULL, 'London', 'Greater London', 'United Kingdom', 'W1D 1BS');

-- 3. Customer Seed (5 Customers)
INSERT INTO [SalesLT].[Customer] ([FirstName], [LastName], [CompanyName], [EmailAddress], [Phone])
VALUES
('Orlando', 'Gee', 'A Bike Store', 'orlando0@adventure-works.com', '245-555-0173'),
('Keith', 'Harris', 'Progressive Sports', 'keith0@adventure-works.com', '170-555-0127'),
('Donna', 'Carreras', 'Advanced Bikes', 'donna0@adventure-works.com', '279-555-0130'),
('Janet', 'Gates', 'Modular Cycle Systems', 'janet1@adventure-works.com', '710-555-0173'),
('Lucy', 'Harrington', 'Metropolitan Sports', 'lucy0@adventure-works.com', '828-555-0186');

-- 4. CustomerAddress Seed
INSERT INTO [SalesLT].[CustomerAddress] ([CustomerID], [AddressID], [AddressType])
VALUES
(1, 1, 'Main Office'),
(2, 2, 'Main Office'),
(3, 3, 'Shipping'),
(4, 4, 'Main Office'),
(5, 5, 'Main Office');

-- 5. ProductCategory Seed (Parent & Child Categories)
INSERT INTO [SalesLT].[ProductCategory] ([ParentProductCategoryID], [Name])
VALUES
(NULL, 'Bikes'),
(NULL, 'Components'),
(NULL, 'Clothing'),
(NULL, 'Accessories');

-- Child categories (ParentProductCategoryID = 1: Bikes)
INSERT INTO [SalesLT].[ProductCategory] ([ParentProductCategoryID], [Name])
VALUES
(1, 'Mountain Bikes'),
(1, 'Road Bikes'),
(1, 'Touring Bikes');

-- Child categories (ParentProductCategoryID = 3: Clothing)
INSERT INTO [SalesLT].[ProductCategory] ([ParentProductCategoryID], [Name])
VALUES
(3, 'Jerseys'),
(3, 'Shorts');

-- 6. ProductModel Seed
INSERT INTO [SalesLT].[ProductModel] ([Name], [CatalogDescription])
VALUES
('Mountain-100', '<HTML>Lightweight aluminium frame mountain bike.</HTML>'),
('Road-150', '<HTML>Aerodynamic carbon fiber road bike.</HTML>'),
('Touring-1000', '<HTML>Durable long-distance touring bike.</HTML>'),
('Classic Jersey', '<HTML>Breathable polyester cycling jersey.</HTML>');

-- 7. ProductDescription Seed
INSERT INTO [SalesLT].[ProductDescription] ([Description])
VALUES
('Top-of-the-line mountain bike with front suspension and disc brakes.'),
('High performance road bicycle built for racing enthusiasts.'),
('Ultimate touring bicycle designed for heavy panniers and comfort.'),
('Quick-drying breathable short-sleeve jersey with moisture control.');

-- 8. Product Seed (6 Products)
INSERT INTO [SalesLT].[Product] ([Name], [ProductNumber], [Color], [StandardCost], [ListPrice], [Size], [Weight], [ProductCategoryID], [ProductModelID], [SellStartDate])
VALUES
('Mountain-100 Silver, 38', 'BK-M18S-38', 'Silver', 700.0000, 1200.0000, '38', 12.50, 5, 1, '2025-01-01'),
('Mountain-100 Black, 42', 'BK-M18B-42', 'Black', 750.0000, 1250.0000, '42', 13.00, 5, 1, '2025-01-01'),
('Road-150 Red, 48', 'BK-R15R-48', 'Red', 900.0000, 1500.0000, '48', 9.20, 6, 2, '2025-02-01'),
('Touring-1000 Blue, 54', 'BK-T10B-54', 'Blue', 800.0000, 1350.0000, '54', 14.10, 7, 3, '2025-02-15'),
('Men Sports Jersey, L', 'SJ-01-L', 'Yellow', 25.0000, 49.9900, 'L', 0.25, 8, 4, '2025-03-01'),
('Men Sports Jersey, M', 'SJ-01-M', 'Yellow', 25.0000, 49.9900, 'M', 0.22, 8, 4, '2025-03-01');

-- 9. ProductModelProductDescription Seed
INSERT INTO [SalesLT].[ProductModelProductDescription] ([ProductModelID], [ProductDescriptionID], [Culture])
VALUES
(1, 1, 'en'),
(2, 2, 'en'),
(3, 3, 'en'),
(4, 4, 'en');

-- 10. SalesOrderHeader Seed (3 Orders)
INSERT INTO [SalesLT].[SalesOrderHeader] 
([RevisionNumber], [OrderDate], [DueDate], [ShipDate], [Status], [OnlineOrderFlag], [SalesOrderNumber], [CustomerID], [ShipToAddressID], [BillToAddressID], [ShipMethod], [SubTotal], [TaxAmt], [Freight], [TotalDue])
VALUES
(1, '2026-01-10', '2026-01-22', '2026-01-17', 5, 1, 'SO71780', 1, 1, 1, 'CARGO TRANSPORT 5', 2450.0000, 196.0000, 61.2500, 2707.2500),
(1, '2026-01-15', '2026-01-27', '2026-01-22', 5, 1, 'SO71781', 2, 2, 2, 'CARGO TRANSPORT 5', 1549.9900, 124.0000, 38.7500, 1712.7400),
(1, '2026-02-01', '2026-02-13', '2026-02-08', 5, 1, 'SO71782', 3, 3, 3, 'CARGO TRANSPORT 5', 1350.0000, 108.0000, 33.7500, 1491.7500);

-- 11. SalesOrderDetail Seed (Line items)
INSERT INTO [SalesLT].[SalesOrderDetail]
([SalesOrderID], [OrderQty], [ProductID], [UnitPrice], [UnitPriceDiscount], [LineTotal])
VALUES
(1, 2, 1, 1200.0000, 0.0000, 2400.0000),
(1, 1, 5, 49.9900, 0.0000, 49.9900),
(2, 1, 3, 1500.0000, 0.0000, 1500.0000),
(2, 1, 6, 49.9900, 0.0000, 49.9900),
(3, 1, 4, 1350.0000, 0.0000, 1350.0000);
