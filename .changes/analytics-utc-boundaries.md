# fixed

Analytics could omit midnight events and assign funnels to the wrong week when
PostgreSQL used a local timezone. Daily counts, active-subject aggregation and
funnel computation now use UTC inside their transactions, including across
daylight-saving changes, without changing the caller's connection setting.
