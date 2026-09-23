CREATE TABLE IF NOT EXISTS mydataset.orders (order_id INT64 NOT NULL, customer_name STRING(100) DEFAULT 'unknown', tags ARRAY<STRUCT<name STRING, value INT64>>, amount NUMERIC(10,2), created DATE CHECK(created > '2000-01-01'), customer_id INT64 REFERENCES customers (id), CONSTRAINT pk_orders PRIMARY KEY(order_id), FOREIGN KEY(customer_name) REFERENCES names(name), UNIQUE(amount), CHECK(amount>0)) PARTITION BY DATE(created) CLUSTER BY order_id, customer_name OPTIONS(description="orders");
create table t (a int)
;
create or replace temporary table
    t2 (
        -- the id
        id int, -- inline
        name text,

        s struct<a int64 not null>
    );
CREATE TABLE orders (order_id BIGINT NOT NULL, customer_identifier_with_long_name BIGINT, region_identifier_long INT, CONSTRAINT fk_orders_customer_region FOREIGN KEY (customer_identifier_with_long_name, region_identifier_long) REFERENCES customers_and_regions(customer_id, region_id) ON DELETE CASCADE);
create table t3 (a_really_long_column_name_that_goes_on_and_on struct<field_one int64, field_two string, field_three array<struct<x int64, y int64>>> not null, b int,) partition by date_trunc(some_really_long_column_name, month) options (description = "a very long description that exceeds the line length");
CREATE TABLE t4 AS SELECT 1 AS a;
create table t5 (a, b) as select 1, 2;
create table t6 like t5;
create table t7 (like t5 including all);
)))))__SQLFMT_OUTPUT__(((((
create table if not exists mydataset.orders (
    order_id int64 not null,
    customer_name string(100) default 'unknown',
    tags array<struct<name string, value int64>>,
    amount numeric(10, 2),
    created date check (created > '2000-01-01'),
    customer_id int64 references customers(id),
    constraint pk_orders primary key (order_id),
    foreign key (customer_name) references names(name),
    unique (amount),
    check (amount > 0)
)
partition by date(created)
cluster by order_id, customer_name
options (description = "orders")
;
create table t (
    a int
)
;
create or replace temporary table t2 (
    -- the id
    id int,  -- inline
    name text,

    s struct<a int64 not null>
)
;
create table orders (
    order_id bigint not null,
    customer_identifier_with_long_name bigint,
    region_identifier_long int,
    constraint fk_orders_customer_region
        foreign key (customer_identifier_with_long_name, region_identifier_long)
        references customers_and_regions(customer_id, region_id) on delete cascade
)
;
create table t3 (
    a_really_long_column_name_that_goes_on_and_on struct<field_one int64, field_two string, field_three array<struct<x int64, y int64>>> not null,
    b int
)
partition by date_trunc(some_really_long_column_name, month)
options (description = "a very long description that exceeds the line length")
;
CREATE TABLE t4 AS SELECT 1 AS a;
create table t5 (a, b) as select 1, 2;
create table t6 like t5;
create table t7 (like t5 including all);
