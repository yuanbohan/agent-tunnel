create table operator_audit_events (
    id bigint generated always as identity primary key,
    event_type text not null,
    actor text not null,
    target_user_id bigint,
    target_username text not null default '',
    target_agent_token_id text not null default '',
    metadata_json jsonb not null default '{}'::jsonb,
    created_at timestamptz not null
);

create index operator_audit_events_event_type_id_idx
    on operator_audit_events(event_type, id);
