-- source: https://cloud.google.com/bigquery/docs/reference/standard-sql/data-definition-language
CREATE TABLE mydataset.newtable
(
  x INT64 OPTIONS(description="An optional INTEGER field"),
  y STRUCT<
    a ARRAY<STRING> OPTIONS(description="A repeated STRING field"),
    b BOOL
  >
)
PARTITION BY _PARTITIONDATE
OPTIONS(
  expiration_timestamp=TIMESTAMP "2025-01-01 00:00:00 UTC",
  partition_expiration_days=1,
  description="a table that expires in 2025, with each partition living for 24 hours",
  labels=[("org_unit", "development")]
);
create table if not exists `my-project.mydataset.newtable` (x int64 not null, y numeric(10,2), z array<string>, primary key (x) not enforced) partition by range_bucket(x, generate_array(0, 100, 10)) cluster by y, z;
create or replace table mydataset.a_really_long_table_name_that_takes_up_space (a_really_long_column_name_for_struct struct<first_field_name string, second_field_name array<int64>>, b int64) partition by timestamp_trunc(some_really_long_timestamp_column_name, hour, "America/Los_Angeles") options (description = "this is a table with a really long description");
)))))__SQLFMT_OUTPUT__(((((
-- source:
-- https://cloud.google.com/bigquery/docs/reference/standard-sql/data-definition-language
create table mydataset.newtable (
    x int64 options(description = "An optional INTEGER field"),
    y struct<a array<string> options(description = "A repeated STRING field"), b bool>
)
partition by _partitiondate
options (expiration_timestamp = timestamp "2025-01-01 00:00:00 UTC", partition_expiration_days = 1, description = "a table that expires in 2025, with each partition living for 24 hours", labels = [("org_unit", "development")])
;
create table if not exists `my-project.mydataset.newtable` (
    x int64 not null,
    y numeric(10, 2),
    z array<string>,
    primary key (x) not enforced
)
partition by range_bucket(x, generate_array(0, 100, 10))
cluster by y, z
;
create or replace table mydataset.a_really_long_table_name_that_takes_up_space (
    a_really_long_column_name_for_struct struct<first_field_name string, second_field_name array<int64>>,
    b int64
)
partition by timestamp_trunc(some_really_long_timestamp_column_name, hour, "America/Los_Angeles")
options (description = "this is a table with a really long description")
;
