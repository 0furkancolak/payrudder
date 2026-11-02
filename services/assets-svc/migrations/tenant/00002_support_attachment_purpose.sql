-- A fifth kind of asset: a file attached to a support ticket (plan 12). The help desk stores it under the first tenant of the account that opened the ticket;
-- ListReadyAssets leaves it out unless asked for by name, and the tenant API cannot name it.

-- +goose Up
ALTER TABLE assets DROP CONSTRAINT assets_purpose_check;
ALTER TABLE assets ADD CONSTRAINT assets_purpose_check CHECK (purpose IN ('invoice_logo','invoice_attachment','general','support_attachment'));

-- +goose Down
DELETE FROM assets WHERE purpose = 'support_attachment';
ALTER TABLE assets DROP CONSTRAINT assets_purpose_check;
ALTER TABLE assets ADD CONSTRAINT assets_purpose_check CHECK (purpose IN ('invoice_logo','invoice_attachment','general'));
