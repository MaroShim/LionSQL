-- ============================================================================
-- Microsoft AdventureWorksLT (Lightweight OLTP) Database Schema
-- Canonical T-SQL DDL for LionSQL Compatibility Verification
-- ============================================================================

-- 1. dbo.ErrorLog
CREATE TABLE [dbo].[ErrorLog] (
    [ErrorLogID] INT IDENTITY(1,1) NOT NULL,
    [ErrorTime] DATETIME NOT NULL DEFAULT (GETDATE()),
    [UserName] VARCHAR(128) NOT NULL,
    [ErrorNumber] INT NOT NULL,
    [ErrorSeverity] INT NULL,
    [ErrorState] INT NULL,
    [ErrorProcedure] VARCHAR(126) NULL,
    [ErrorLine] INT NULL,
    [ErrorMessage] VARCHAR(4000) NOT NULL,
    CONSTRAINT [PK_ErrorLog_ErrorLogID] PRIMARY KEY ([ErrorLogID])
);

-- 2. SalesLT.Address
CREATE TABLE [SalesLT].[Address] (
    [AddressID] INT IDENTITY(1,1) NOT NULL,
    [AddressLine1] VARCHAR(60) NOT NULL,
    [AddressLine2] VARCHAR(60) NULL,
    [City] VARCHAR(30) NOT NULL,
    [StateProvince] VARCHAR(50) NOT NULL,
    [CountryRegion] VARCHAR(50) NOT NULL,
    [PostalCode] VARCHAR(15) NOT NULL,
    [rowguid] UNIQUEIDENTIFIER NOT NULL DEFAULT (NEWID()),
    [ModifiedDate] DATETIME NOT NULL DEFAULT (GETDATE()),
    CONSTRAINT [PK_Address_AddressID] PRIMARY KEY ([AddressID])
);

-- 3. SalesLT.Customer
CREATE TABLE [SalesLT].[Customer] (
    [CustomerID] INT IDENTITY(1,1) NOT NULL,
    [NameStyle] BIT NOT NULL DEFAULT (0),
    [Title] VARCHAR(8) NULL,
    [FirstName] VARCHAR(50) NOT NULL,
    [MiddleName] VARCHAR(50) NULL,
    [LastName] VARCHAR(50) NOT NULL,
    [Suffix] VARCHAR(10) NULL,
    [CompanyName] VARCHAR(128) NULL,
    [SalesPerson] VARCHAR(256) NULL,
    [EmailAddress] VARCHAR(50) NULL,
    [Phone] VARCHAR(25) NULL,
    [PasswordHash] VARCHAR(128) NOT NULL DEFAULT ('$2a$12$dummyHashPlaceholderForSeedTesting0000000000000000000000000'),
    [PasswordSalt] VARCHAR(10) NOT NULL DEFAULT ('dummysalt1'),
    [rowguid] UNIQUEIDENTIFIER NOT NULL DEFAULT (NEWID()),
    [ModifiedDate] DATETIME NOT NULL DEFAULT (GETDATE()),
    CONSTRAINT [PK_Customer_CustomerID] PRIMARY KEY ([CustomerID])
);

-- 4. SalesLT.CustomerAddress
CREATE TABLE [SalesLT].[CustomerAddress] (
    [CustomerID] INT NOT NULL,
    [AddressID] INT NOT NULL,
    [AddressType] VARCHAR(50) NOT NULL,
    [rowguid] UNIQUEIDENTIFIER NOT NULL DEFAULT (NEWID()),
    [ModifiedDate] DATETIME NOT NULL DEFAULT (GETDATE()),
    CONSTRAINT [PK_CustomerAddress_CustomerID_AddressID] PRIMARY KEY ([CustomerID], [AddressID]),
    CONSTRAINT [FK_CustomerAddress_Customer_CustomerID] FOREIGN KEY ([CustomerID]) REFERENCES [SalesLT].[Customer] ([CustomerID]),
    CONSTRAINT [FK_CustomerAddress_Address_AddressID] FOREIGN KEY ([AddressID]) REFERENCES [SalesLT].[Address] ([AddressID])
);

-- 5. SalesLT.ProductCategory
CREATE TABLE [SalesLT].[ProductCategory] (
    [ProductCategoryID] INT IDENTITY(1,1) NOT NULL,
    [ParentProductCategoryID] INT NULL,
    [Name] VARCHAR(50) NOT NULL,
    [rowguid] UNIQUEIDENTIFIER NOT NULL DEFAULT (NEWID()),
    [ModifiedDate] DATETIME NOT NULL DEFAULT (GETDATE()),
    CONSTRAINT [PK_ProductCategory_ProductCategoryID] PRIMARY KEY ([ProductCategoryID]),
    CONSTRAINT [FK_ProductCategory_ProductCategory_ParentProductCategoryID] FOREIGN KEY ([ParentProductCategoryID]) REFERENCES [SalesLT].[ProductCategory] ([ProductCategoryID])
);

-- 6. SalesLT.ProductModel
CREATE TABLE [SalesLT].[ProductModel] (
    [ProductModelID] INT IDENTITY(1,1) NOT NULL,
    [Name] VARCHAR(50) NOT NULL,
    [CatalogDescription] VARCHAR(MAX) NULL,
    [rowguid] UNIQUEIDENTIFIER NOT NULL DEFAULT (NEWID()),
    [ModifiedDate] DATETIME NOT NULL DEFAULT (GETDATE()),
    CONSTRAINT [PK_ProductModel_ProductModelID] PRIMARY KEY ([ProductModelID])
);

-- 7. SalesLT.ProductDescription
CREATE TABLE [SalesLT].[ProductDescription] (
    [ProductDescriptionID] INT IDENTITY(1,1) NOT NULL,
    [Description] VARCHAR(400) NOT NULL,
    [rowguid] UNIQUEIDENTIFIER NOT NULL DEFAULT (NEWID()),
    [ModifiedDate] DATETIME NOT NULL DEFAULT (GETDATE()),
    CONSTRAINT [PK_ProductDescription_ProductDescriptionID] PRIMARY KEY ([ProductDescriptionID])
);

-- 8. SalesLT.Product
CREATE TABLE [SalesLT].[Product] (
    [ProductID] INT IDENTITY(1,1) NOT NULL,
    [Name] VARCHAR(50) NOT NULL,
    [ProductNumber] VARCHAR(25) NOT NULL,
    [Color] VARCHAR(15) NULL,
    [StandardCost] DECIMAL(18,4) NOT NULL,
    [ListPrice] DECIMAL(18,4) NOT NULL,
    [Size] VARCHAR(5) NULL,
    [Weight] DECIMAL(8,2) NULL,
    [ProductCategoryID] INT NULL,
    [ProductModelID] INT NULL,
    [SellStartDate] DATETIME NOT NULL,
    [SellEndDate] DATETIME NULL,
    [DiscontinuedDate] DATETIME NULL,
    [ThumbNailPhoto] VARCHAR(MAX) NULL,
    [ThumbnailPhotoFileName] VARCHAR(50) NULL,
    [rowguid] UNIQUEIDENTIFIER NOT NULL DEFAULT (NEWID()),
    [ModifiedDate] DATETIME NOT NULL DEFAULT (GETDATE()),
    CONSTRAINT [PK_Product_ProductID] PRIMARY KEY ([ProductID]),
    CONSTRAINT [FK_Product_ProductCategory_ProductCategoryID] FOREIGN KEY ([ProductCategoryID]) REFERENCES [SalesLT].[ProductCategory] ([ProductCategoryID]),
    CONSTRAINT [FK_Product_ProductModel_ProductModelID] FOREIGN KEY ([ProductModelID]) REFERENCES [SalesLT].[ProductModel] ([ProductModelID]),
    CONSTRAINT [CK_Product_StandardCost] CHECK ([StandardCost] >= 0.00),
    CONSTRAINT [CK_Product_ListPrice] CHECK ([ListPrice] >= 0.00)
);

-- 9. SalesLT.ProductModelProductDescription
CREATE TABLE [SalesLT].[ProductModelProductDescription] (
    [ProductModelID] INT NOT NULL,
    [ProductDescriptionID] INT NOT NULL,
    [Culture] VARCHAR(6) NOT NULL,
    [rowguid] UNIQUEIDENTIFIER NOT NULL DEFAULT (NEWID()),
    [ModifiedDate] DATETIME NOT NULL DEFAULT (GETDATE()),
    CONSTRAINT [PK_ProductModelProductDescription_ProductModelID_ProductDescriptionID_Culture] PRIMARY KEY ([ProductModelID], [ProductDescriptionID], [Culture]),
    CONSTRAINT [FK_ProductModelProductDescription_ProductModel_ProductModelID] FOREIGN KEY ([ProductModelID]) REFERENCES [SalesLT].[ProductModel] ([ProductModelID]),
    CONSTRAINT [FK_ProductModelProductDescription_ProductDescription_ProductDescriptionID] FOREIGN KEY ([ProductDescriptionID]) REFERENCES [SalesLT].[ProductDescription] ([ProductDescriptionID])
);

-- 10. SalesLT.SalesOrderHeader
CREATE TABLE [SalesLT].[SalesOrderHeader] (
    [SalesOrderID] INT IDENTITY(1,1) NOT NULL,
    [RevisionNumber] TINYINT NOT NULL DEFAULT (0),
    [OrderDate] DATETIME NOT NULL DEFAULT (GETDATE()),
    [DueDate] DATETIME NOT NULL,
    [ShipDate] DATETIME NULL,
    [Status] TINYINT NOT NULL DEFAULT (1),
    [OnlineOrderFlag] BIT NOT NULL DEFAULT (1),
    [SalesOrderNumber] VARCHAR(25) NOT NULL,
    [PurchaseOrderNumber] VARCHAR(25) NULL,
    [AccountNumber] VARCHAR(15) NULL,
    [CustomerID] INT NOT NULL,
    [ShipToAddressID] INT NULL,
    [BillToAddressID] INT NULL,
    [ShipMethod] VARCHAR(50) NOT NULL,
    [CreditCardApprovalCode] VARCHAR(15) NULL,
    [SubTotal] DECIMAL(18,4) NOT NULL DEFAULT (0.00),
    [TaxAmt] DECIMAL(18,4) NOT NULL DEFAULT (0.00),
    [Freight] DECIMAL(18,4) NOT NULL DEFAULT (0.00),
    [TotalDue] DECIMAL(18,4) NOT NULL DEFAULT (0.00),
    [Comment] VARCHAR(MAX) NULL,
    [rowguid] UNIQUEIDENTIFIER NOT NULL DEFAULT (NEWID()),
    [ModifiedDate] DATETIME NOT NULL DEFAULT (GETDATE()),
    CONSTRAINT [PK_SalesOrderHeader_SalesOrderID] PRIMARY KEY ([SalesOrderID]),
    CONSTRAINT [FK_SalesOrderHeader_Customer_CustomerID] FOREIGN KEY ([CustomerID]) REFERENCES [SalesLT].[Customer] ([CustomerID]),
    CONSTRAINT [FK_SalesOrderHeader_Address_BillTo_AddressID] FOREIGN KEY ([BillToAddressID]) REFERENCES [SalesLT].[Address] ([AddressID]),
    CONSTRAINT [FK_SalesOrderHeader_Address_ShipTo_AddressID] FOREIGN KEY ([ShipToAddressID]) REFERENCES [SalesLT].[Address] ([AddressID])
);

-- 11. SalesLT.SalesOrderDetail
CREATE TABLE [SalesLT].[SalesOrderDetail] (
    [SalesOrderID] INT NOT NULL,
    [SalesOrderDetailID] INT IDENTITY(1,1) NOT NULL,
    [OrderQty] SMALLINT NOT NULL,
    [ProductID] INT NOT NULL,
    [UnitPrice] DECIMAL(18,4) NOT NULL,
    [UnitPriceDiscount] DECIMAL(18,4) NOT NULL DEFAULT (0.00),
    [LineTotal] DECIMAL(38,6) NOT NULL,
    [rowguid] UNIQUEIDENTIFIER NOT NULL DEFAULT (NEWID()),
    [ModifiedDate] DATETIME NOT NULL DEFAULT (GETDATE()),
    CONSTRAINT [PK_SalesOrderDetail_SalesOrderID_SalesOrderDetailID] PRIMARY KEY ([SalesOrderID], [SalesOrderDetailID]),
    CONSTRAINT [FK_SalesOrderDetail_SalesOrderHeader_SalesOrderID] FOREIGN KEY ([SalesOrderID]) REFERENCES [SalesLT].[SalesOrderHeader] ([SalesOrderID]),
    CONSTRAINT [FK_SalesOrderDetail_Product_ProductID] FOREIGN KEY ([ProductID]) REFERENCES [SalesLT].[Product] ([ProductID]),
    CONSTRAINT [CK_SalesOrderDetail_OrderQty] CHECK ([OrderQty] > 0),
    CONSTRAINT [CK_SalesOrderDetail_UnitPrice] CHECK ([UnitPrice] >= 0.00),
    CONSTRAINT [CK_SalesOrderDetail_UnitPriceDiscount] CHECK ([UnitPriceDiscount] >= 0.00)
);
