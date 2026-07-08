-- +goose Up
-- +goose StatementBegin
ALTER TABLE statements DROP CONSTRAINT IF EXISTS statements_subject_id_fkey;
ALTER TABLE statements DROP CONSTRAINT IF EXISTS statements_property_id_fkey;
ALTER TABLE statements DROP CONSTRAINT IF EXISTS statements_val_entity_fkey;
ALTER TABLE qualifiers DROP CONSTRAINT IF EXISTS qualifiers_statement_id_fkey;
ALTER TABLE qualifiers DROP CONSTRAINT IF EXISTS qualifiers_property_id_fkey;
ALTER TABLE qualifiers DROP CONSTRAINT IF EXISTS qualifiers_val_entity_fkey;
ALTER TABLE relations DROP CONSTRAINT IF EXISTS relations_source_id_fkey;
ALTER TABLE relations DROP CONSTRAINT IF EXISTS relations_target_id_fkey;
ALTER TABLE relations DROP CONSTRAINT IF EXISTS relations_statement_id_fkey;
ALTER TABLE statement_sources DROP CONSTRAINT IF EXISTS statement_sources_statement_id_fkey;
ALTER TABLE statement_sources DROP CONSTRAINT IF EXISTS statement_sources_source_id_fkey;
ALTER TABLE source_verifications DROP CONSTRAINT IF EXISTS source_verifications_source_id_fkey;
ALTER TABLE proposed_properties DROP CONSTRAINT IF EXISTS proposed_properties_example_source_id_fkey;
ALTER TABLE dossiers DROP CONSTRAINT IF EXISTS dossiers_entity_id_fkey;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_entities_tenant_id') THEN
        ALTER TABLE entities
            ADD CONSTRAINT uq_entities_tenant_id UNIQUE (tenant_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_properties_tenant_id') THEN
        ALTER TABLE properties
            ADD CONSTRAINT uq_properties_tenant_id UNIQUE (tenant_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_statements_tenant_id') THEN
        ALTER TABLE statements
            ADD CONSTRAINT uq_statements_tenant_id UNIQUE (tenant_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_relations_tenant_id') THEN
        ALTER TABLE relations
            ADD CONSTRAINT uq_relations_tenant_id UNIQUE (tenant_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'uq_sources_tenant_id') THEN
        ALTER TABLE sources
            ADD CONSTRAINT uq_sources_tenant_id UNIQUE (tenant_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_statements_subject_tenant') THEN
        ALTER TABLE statements
            ADD CONSTRAINT fk_statements_subject_tenant
                FOREIGN KEY (tenant_id, subject_id)
                REFERENCES entities (tenant_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_statements_val_entity_tenant') THEN
        ALTER TABLE statements
            ADD CONSTRAINT fk_statements_val_entity_tenant
                FOREIGN KEY (tenant_id, val_entity)
                REFERENCES entities (tenant_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_qualifiers_statement') THEN
        ALTER TABLE qualifiers
            ADD CONSTRAINT fk_qualifiers_statement
                FOREIGN KEY (statement_id)
                REFERENCES statements (id) ON DELETE CASCADE;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_relations_source_tenant') THEN
        ALTER TABLE relations
            ADD CONSTRAINT fk_relations_source_tenant
                FOREIGN KEY (tenant_id, source_id)
                REFERENCES entities (tenant_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_relations_target_tenant') THEN
        ALTER TABLE relations
            ADD CONSTRAINT fk_relations_target_tenant
                FOREIGN KEY (tenant_id, target_id)
                REFERENCES entities (tenant_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_relations_statement_tenant') THEN
        ALTER TABLE relations
            ADD CONSTRAINT fk_relations_statement_tenant
                FOREIGN KEY (tenant_id, statement_id)
                REFERENCES statements (tenant_id, id) ON DELETE CASCADE;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_statement_sources_statement_tenant') THEN
        ALTER TABLE statement_sources
            ADD CONSTRAINT fk_statement_sources_statement_tenant
                FOREIGN KEY (tenant_id, statement_id)
                REFERENCES statements (tenant_id, id) ON DELETE CASCADE;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_statement_sources_source_tenant') THEN
        ALTER TABLE statement_sources
            ADD CONSTRAINT fk_statement_sources_source_tenant
                FOREIGN KEY (tenant_id, source_id)
                REFERENCES sources (tenant_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_source_verifications_source_tenant') THEN
        ALTER TABLE source_verifications
            ADD CONSTRAINT fk_source_verifications_source_tenant
                FOREIGN KEY (tenant_id, source_id)
                REFERENCES sources (tenant_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_proposed_properties_source_tenant') THEN
        ALTER TABLE proposed_properties
            ADD CONSTRAINT fk_proposed_properties_source_tenant
                FOREIGN KEY (tenant_id, example_source_id)
                REFERENCES sources (tenant_id, id);
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_dossiers_entity_tenant') THEN
        ALTER TABLE dossiers
            ADD CONSTRAINT fk_dossiers_entity_tenant
                FOREIGN KEY (tenant_id, entity_id)
                REFERENCES entities (tenant_id, id);
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION enforce_statement_property_scope()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    property_tenant UUID;
BEGIN
    SELECT tenant_id INTO property_tenant
    FROM properties
    WHERE id = NEW.property_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'property % does not exist', NEW.property_id
            USING ERRCODE = '23503';
    END IF;

    IF property_tenant IS NOT NULL AND property_tenant <> NEW.tenant_id THEN
        RAISE EXCEPTION 'tenant % cannot reference property % owned by tenant %', NEW.tenant_id, NEW.property_id, property_tenant
            USING ERRCODE = '23503';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_statements_property_scope ON statements;
CREATE TRIGGER trg_statements_property_scope
    BEFORE INSERT OR UPDATE OF tenant_id, property_id ON statements
    FOR EACH ROW EXECUTE FUNCTION enforce_statement_property_scope();

CREATE OR REPLACE FUNCTION enforce_qualifier_scope()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    statement_tenant UUID;
    property_tenant  UUID;
    entity_tenant    UUID;
BEGIN
    SELECT tenant_id INTO statement_tenant
    FROM statements
    WHERE id = NEW.statement_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'statement % does not exist', NEW.statement_id
            USING ERRCODE = '23503';
    END IF;

    SELECT tenant_id INTO property_tenant
    FROM properties
    WHERE id = NEW.property_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'property % does not exist', NEW.property_id
            USING ERRCODE = '23503';
    END IF;

    IF property_tenant IS NOT NULL AND property_tenant <> statement_tenant THEN
        RAISE EXCEPTION 'statement tenant % cannot reference property % owned by tenant %', statement_tenant, NEW.property_id, property_tenant
            USING ERRCODE = '23503';
    END IF;

    IF NEW.val_entity IS NOT NULL THEN
        SELECT tenant_id INTO entity_tenant
        FROM entities
        WHERE id = NEW.val_entity;

        IF NOT FOUND THEN
            RAISE EXCEPTION 'entity % does not exist', NEW.val_entity
                USING ERRCODE = '23503';
        END IF;

        IF entity_tenant <> statement_tenant THEN
            RAISE EXCEPTION 'statement tenant % cannot reference entity % owned by tenant %', statement_tenant, NEW.val_entity, entity_tenant
                USING ERRCODE = '23503';
        END IF;
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_qualifiers_scope ON qualifiers;
CREATE TRIGGER trg_qualifiers_scope
    BEFORE INSERT OR UPDATE OF statement_id, property_id, val_entity ON qualifiers
    FOR EACH ROW EXECUTE FUNCTION enforce_qualifier_scope();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_qualifiers_scope ON qualifiers;
DROP TRIGGER IF EXISTS trg_statements_property_scope ON statements;
DROP FUNCTION IF EXISTS enforce_qualifier_scope();
DROP FUNCTION IF EXISTS enforce_statement_property_scope();

ALTER TABLE dossiers DROP CONSTRAINT IF EXISTS fk_dossiers_entity_tenant;
ALTER TABLE proposed_properties DROP CONSTRAINT IF EXISTS fk_proposed_properties_source_tenant;
ALTER TABLE source_verifications DROP CONSTRAINT IF EXISTS fk_source_verifications_source_tenant;
ALTER TABLE statement_sources DROP CONSTRAINT IF EXISTS fk_statement_sources_source_tenant;
ALTER TABLE statement_sources DROP CONSTRAINT IF EXISTS fk_statement_sources_statement_tenant;
ALTER TABLE relations DROP CONSTRAINT IF EXISTS fk_relations_statement_tenant;
ALTER TABLE relations DROP CONSTRAINT IF EXISTS fk_relations_target_tenant;
ALTER TABLE relations DROP CONSTRAINT IF EXISTS fk_relations_source_tenant;
ALTER TABLE qualifiers DROP CONSTRAINT IF EXISTS fk_qualifiers_statement;
ALTER TABLE statements DROP CONSTRAINT IF EXISTS fk_statements_val_entity_tenant;
ALTER TABLE statements DROP CONSTRAINT IF EXISTS fk_statements_subject_tenant;

ALTER TABLE sources DROP CONSTRAINT IF EXISTS uq_sources_tenant_id;
ALTER TABLE relations DROP CONSTRAINT IF EXISTS uq_relations_tenant_id;
ALTER TABLE statements DROP CONSTRAINT IF EXISTS uq_statements_tenant_id;
ALTER TABLE properties DROP CONSTRAINT IF EXISTS uq_properties_tenant_id;
ALTER TABLE entities DROP CONSTRAINT IF EXISTS uq_entities_tenant_id;

ALTER TABLE statements
    ADD CONSTRAINT statements_subject_id_fkey FOREIGN KEY (subject_id) REFERENCES entities (id),
    ADD CONSTRAINT statements_property_id_fkey FOREIGN KEY (property_id) REFERENCES properties (id),
    ADD CONSTRAINT statements_val_entity_fkey FOREIGN KEY (val_entity) REFERENCES entities (id);

ALTER TABLE qualifiers
    ADD CONSTRAINT qualifiers_statement_id_fkey FOREIGN KEY (statement_id) REFERENCES statements (id) ON DELETE CASCADE,
    ADD CONSTRAINT qualifiers_property_id_fkey FOREIGN KEY (property_id) REFERENCES properties (id),
    ADD CONSTRAINT qualifiers_val_entity_fkey FOREIGN KEY (val_entity) REFERENCES entities (id);

ALTER TABLE relations
    ADD CONSTRAINT relations_source_id_fkey FOREIGN KEY (source_id) REFERENCES entities (id),
    ADD CONSTRAINT relations_target_id_fkey FOREIGN KEY (target_id) REFERENCES entities (id),
    ADD CONSTRAINT relations_statement_id_fkey FOREIGN KEY (statement_id) REFERENCES statements (id) ON DELETE CASCADE;

ALTER TABLE statement_sources
    ADD CONSTRAINT statement_sources_statement_id_fkey FOREIGN KEY (statement_id) REFERENCES statements (id) ON DELETE CASCADE,
    ADD CONSTRAINT statement_sources_source_id_fkey FOREIGN KEY (source_id) REFERENCES sources (id);

ALTER TABLE source_verifications
    ADD CONSTRAINT source_verifications_source_id_fkey FOREIGN KEY (source_id) REFERENCES sources (id);

ALTER TABLE proposed_properties
    ADD CONSTRAINT proposed_properties_example_source_id_fkey FOREIGN KEY (example_source_id) REFERENCES sources (id);

ALTER TABLE dossiers
    ADD CONSTRAINT dossiers_entity_id_fkey FOREIGN KEY (entity_id) REFERENCES entities (id);
-- +goose StatementEnd
