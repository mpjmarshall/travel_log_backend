-- Down for 0007. The column comes back and every traveller has an empty one,
-- because a hash cannot be recovered from a table it was deleted from. Anybody
-- who signed in with a passphrase before will have to be given a new one out
-- of band.
ALTER TABLE travellers ADD COLUMN passphrase_hash text NOT NULL DEFAULT '';
