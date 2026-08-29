-- Down for 0005. The table holds nothing that outlives a sign-in: every row
-- is a code that expires within minutes, so dropping it costs a traveller
-- mid-sign-in one retry and costs nobody anything else.
DROP TABLE sign_in_codes;
