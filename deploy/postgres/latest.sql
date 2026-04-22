begin;

create table if not exists users (
    id bigint generated always as identity primary key,
    username text not null,
    username_norm text not null unique,
    password_hash text not null,
    created_at timestamptz not null,
    updated_at timestamptz not null
);

create table if not exists invite_codes (
    id bigint generated always as identity primary key,
    code text not null unique,
    created_by text not null,
    created_at timestamptz not null,
    expires_at timestamptz not null,
    disabled_at timestamptz,
    disabled_by text,
    consumed_at timestamptz,
    consumed_by_user_id bigint references users(id) on delete set null,
    consumed_by_username text
);

create index if not exists invite_codes_created_at_idx on invite_codes(created_at desc);
create index if not exists invite_codes_consumed_at_idx on invite_codes(consumed_at);

create table if not exists app_sessions (
    id text primary key,
    user_id bigint not null references users(id) on delete cascade,
    access_token_digest text not null unique,
    access_expires_at timestamptz not null,
    refresh_token_digest text not null unique,
    refresh_expires_at timestamptz not null,
    revoked_at timestamptz,
    revoke_reason text not null default '',
    created_at timestamptz not null,
    updated_at timestamptz not null
);

create index if not exists app_sessions_user_id_idx on app_sessions(user_id);
create index if not exists app_sessions_access_expires_at_idx on app_sessions(access_expires_at);
create index if not exists app_sessions_refresh_expires_at_idx on app_sessions(refresh_expires_at);

create table if not exists agent_tokens (
    id text primary key,
    user_id bigint not null references users(id) on delete cascade,
    name text not null,
    token_digest text not null unique,
    created_at timestamptz not null,
    updated_at timestamptz not null,
    last_used_at timestamptz,
    revoked_at timestamptz,
    revoke_reason text not null default ''
);

create index if not exists agent_tokens_user_id_idx on agent_tokens(user_id);
create index if not exists agent_tokens_last_used_at_idx on agent_tokens(last_used_at);

create table if not exists operator_audit_events (
    id bigint generated always as identity primary key,
    event_type text not null,
    actor text not null,
    target_user_id bigint,
    target_username text not null default '',
    target_agent_token_id text not null default '',
    metadata_json jsonb not null default '{}'::jsonb,
    created_at timestamptz not null
);

create index if not exists operator_audit_events_event_type_id_idx
    on operator_audit_events(event_type, id);

commit;
