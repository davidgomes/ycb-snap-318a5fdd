CREATE TABLE films (
    code        char(5) CONSTRAINT firstkey PRIMARY KEY,
    title       varchar(40) NOT NULL,
    did         integer NOT NULL,
    date_prod   date,
    kind        varchar(10),
    len         interval hour to minute
);
CREATE TABLE IF NOT EXISTS my_db.Orders (id INT64 NOT NULL, name STRING(100) DEFAULT 'x', amounts ARRAY<STRUCT<a INT64, b NUMERIC(10,2)>>, cust_id INT64 REFERENCES customers(id), qty INT64 CHECK(qty > 0), PRIMARY KEY(id), CONSTRAINT fk FOREIGN KEY(cust_id) REFERENCES customers(id), CHECK(qty < 100), UNIQUE(name)) PARTITION BY DATE(created_at) CLUSTER BY id, name OPTIONS(description='hi', x=1);
create table t (a int, constraint a_very_long_constraint_name_for_testing_wrapping foreign key (col_one, col_two) references other_schema.other_table(col_one, col_two))
)))))__SQLFMT_OUTPUT__(((((
create table films (
    code char(5) constraint firstkey primary key,
    title varchar(40) not null,
    did integer not null,
    date_prod date,
    kind varchar(10),
    len interval hour to minute
)
;
create table if not exists my_db.orders (
    id int64 not null,
    name string(100) default 'x',
    amounts array<struct<a int64, b numeric(10, 2)>>,
    cust_id int64 references customers(id),
    qty int64 check (qty > 0),
    primary key (id),
    constraint fk foreign key (cust_id) references customers(id),
    check (qty < 100),
    unique (name)
)
partition by date(created_at)
cluster by id, name
options(description = 'hi', x = 1)
;
create table t (
    a int,
    constraint a_very_long_constraint_name_for_testing_wrapping
        foreign key (col_one, col_two)
        references other_schema.other_table(col_one, col_two)
)
