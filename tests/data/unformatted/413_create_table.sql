CREATE TABLE IF NOT EXISTS My_Dataset.Films
(
    Code        CHAR(5) CONSTRAINT firstkey PRIMARY KEY,
    title       VARCHAR(40) NOT NULL, -- the title
    did         INTEGER NOT NULL DEFAULT 0 CHECK(did>0),
    price       NUMERIC(10,2) NULL,
    tags        ARRAY<STRUCT<name STRING, vals ARRAY<INT64>>>
    , date_prod DATE
    , len       INTERVAL HOUR TO MINUTE,
    distributor_id INTEGER REFERENCES distributors (did),
    a_very_long_column_name_that_goes_on_and_on VARCHAR(255) DEFAULT 'some long default value' NOT NULL,
    PRIMARY KEY(Code,title),
    CONSTRAINT fk_did FOREIGN KEY(did) REFERENCES distributors (did) ON DELETE CASCADE,
    constraint fk_a_very_long_constraint_name foreign key (distributor_id, did) references distributors(distributor_id, did) on delete cascade,
    UNIQUE(title),
    CHECK(price > 0 AND did IN (1,2))
) PARTITION BY DATE(date_prod) CLUSTER BY Code, title OPTIONS (description="films table");
create table foo (a int, b int);
create table foo as (select 1);
create table bar like baz;
create table baz (a int) as select 1 as a;
)))))__SQLFMT_OUTPUT__(((((
create table if not exists my_dataset.films (
    code char(5) constraint firstkey primary key,
    title varchar(40) not null,  -- the title
    did integer not null default 0 check (did > 0),
    price numeric(10, 2) null,
    tags array<struct<name string, vals array<int64>>>,
    date_prod date,
    len interval hour to minute,
    distributor_id integer references distributors(did),
    a_very_long_column_name_that_goes_on_and_on varchar(255) default 'some long default value' not null,
    primary key (code, title),
    constraint fk_did foreign key (did) references distributors(did) on delete cascade,
    constraint fk_a_very_long_constraint_name foreign key (distributor_id, did)
        references distributors(distributor_id, did) on delete cascade,
    unique (title),
    check (price > 0 and did in (1, 2))
)
partition by date(date_prod)
cluster by code, title
options(description = "films table")
;
create table foo (
    a int,
    b int
)
;
create table foo as (select 1);
create table bar like baz;
create table baz (a int) as select 1 as a;
