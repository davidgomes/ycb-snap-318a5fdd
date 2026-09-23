create table t1 as select * from t2;
CREATE TABLE t1 (a int, b int) AS SELECT 1, 2;
create table t1 like t2;
create table t1 (like t2 including all);
create table t1 (a int) with (fillfactor=70);
create table t1 (a int) engine = MergeTree order by a;
create table t (a int, b int) partition by a as select 1, 2;
)))))__SQLFMT_OUTPUT__(((((
create table t1 as select * from t2;
CREATE TABLE t1 (a int, b int) AS SELECT 1, 2;
create table t1 like t2;
create table t1 (like t2 including all);
create table t1 (a int) with (fillfactor=70);
create table t1 (a int) engine = MergeTree order by a;
create table t (a int, b int) partition by a as select 1, 2;
