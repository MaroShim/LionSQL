-- ============================================================================
-- Microsoft AdventureWorksLT Verification Queries
-- Real-world T-SQL Queries for LionSQL Compatibility Verification
-- ============================================================================

-- Q1: Total Order Amount and Count by Customer (INNER JOIN + GROUP BY + SUM)
SELECT 
    c.CustomerID,
    c.FirstName,
    c.LastName,
    c.CompanyName,
    COUNT(h.SalesOrderID) AS TotalOrders,
    SUM(h.TotalDue) AS CumulativeSpend
FROM [SalesLT].[Customer] c
INNER JOIN [SalesLT].[SalesOrderHeader] h ON c.CustomerID = h.CustomerID
GROUP BY c.CustomerID, c.FirstName, c.LastName, c.CompanyName
ORDER BY CumulativeSpend DESC;

-- Q2: Product Category Hierarchy (Self Join Parent to Child)
SELECT 
    p.Name AS ParentCategory,
    c.Name AS SubCategory
FROM [SalesLT].[ProductCategory] c
INNER JOIN [SalesLT].[ProductCategory] p ON c.ParentProductCategoryID = p.ProductCategoryID
ORDER BY p.Name, c.Name;

-- Q3: Order Line Items with Product Names and Margins
SELECT 
    h.SalesOrderNumber,
    d.SalesOrderDetailID,
    p.Name AS ProductName,
    p.ProductNumber,
    d.OrderQty,
    d.UnitPrice,
    p.StandardCost,
    (d.UnitPrice - p.StandardCost) * d.OrderQty AS EstimatedProfit
FROM [SalesLT].[SalesOrderHeader] h
INNER JOIN [SalesLT].[SalesOrderDetail] d ON h.SalesOrderID = d.SalesOrderID
INNER JOIN [SalesLT].[Product] p ON d.ProductID = p.ProductID
ORDER BY h.SalesOrderNumber, d.SalesOrderDetailID;

-- Q4: Category Aggregations (Product Count, Avg ListPrice, Min/Max Price)
SELECT 
    cat.Name AS CategoryName,
    COUNT(p.ProductID) AS ProductCount,
    AVG(p.ListPrice) AS AvgPrice,
    MIN(p.ListPrice) AS MinPrice,
    MAX(p.ListPrice) AS MaxPrice
FROM [SalesLT].[ProductCategory] cat
LEFT JOIN [SalesLT].[Product] p ON cat.ProductCategoryID = p.ProductCategoryID
GROUP BY cat.Name
ORDER BY ProductCount DESC;

-- Q5: Customer and Shipping Address Details
SELECT 
    c.CompanyName,
    c.FirstName + ' ' + c.LastName AS CustomerName,
    a.AddressLine1,
    a.City,
    a.StateProvince,
    a.CountryRegion,
    a.PostalCode
FROM [SalesLT].[Customer] c
INNER JOIN [SalesLT].[CustomerAddress] ca ON c.CustomerID = ca.CustomerID
INNER JOIN [SalesLT].[Address] a ON ca.AddressID = a.AddressID
ORDER BY c.CompanyName;

-- Q6: Subquery with EXISTS: Find Customers who have placed orders
SELECT 
    c.CustomerID,
    c.CompanyName,
    c.EmailAddress
FROM [SalesLT].[Customer] c
WHERE EXISTS (
    SELECT 1 
    FROM [SalesLT].[SalesOrderHeader] h 
    WHERE h.CustomerID = c.CustomerID
)
ORDER BY c.CustomerID;

-- Q7: CASE WHEN Order Status Decoder
SELECT 
    SalesOrderNumber,
    TotalDue,
    CASE [Status]
        WHEN 1 THEN 'InProcess'
        WHEN 2 THEN 'Approved'
        WHEN 3 THEN 'Backordered'
        WHEN 4 THEN 'Rejected'
        WHEN 5 THEN 'Shipped'
        WHEN 6 THEN 'Cancelled'
        ELSE 'Unknown'
    END AS StatusText
FROM [SalesLT].[SalesOrderHeader]
ORDER BY SalesOrderID;

-- Q8: CTE for High-Value Products (ListPrice > 1000)
WITH HighValueBikes AS (
    SELECT 
        ProductID,
        Name,
        ListPrice,
        Color
    FROM [SalesLT].[Product]
    WHERE ListPrice > 1000.00
)
SELECT 
    ProductID,
    Name,
    Color,
    ListPrice
FROM HighValueBikes
ORDER BY ListPrice DESC;
