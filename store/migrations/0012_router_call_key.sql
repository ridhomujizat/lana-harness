-- Spec 75 REQ-PAF-005: the secret name of the API key that served a call, when
-- the provider has a key pool. NULL is a call made with the provider's only
-- key, a subscription account, or a record stored before this column existed.
ALTER TABLE router_calls ADD COLUMN key TEXT;
