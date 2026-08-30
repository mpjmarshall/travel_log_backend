-- Down for 0006. Dropping the table reopens registration to anybody who can
-- reach the route, so this is not a step to take on a deployed instance.
DROP TABLE invite_codes;
