alter table users
    add column if not exists subscription_tier text not null default 'free';

alter table users
    add constraint users_subscription_tier_check
    check (subscription_tier in ('free', 'pro'));

alter table app_sessions
    add column if not exists device_fingerprint text not null default '';

create index if not exists app_sessions_user_device_fingerprint_idx
    on app_sessions(user_id, device_fingerprint)
    where device_fingerprint <> '';
