# Schema drift report

- generated code source: `development`
- gate target: `production`
- comparison mode: all environments

## development -> staging

- differences: 6 (critical 2, high 3, medium 1, low 0)
- incompatible (code generated from development against staging): 3

### Table and column differences

#### legacy
- `critical` `gate` `table_shape_changed`: no shared columns; development=[a, b], staging=[c, d]

#### users
- `high` `unique_changed`: removed in staging: unique(name=users_email_uq, columns=[email])
- `medium` `default_changed` column `email`: default: 'dev@example.com' -> 'staging@example.com'
- `high` `gate` `nullability_changed` column `email`: nullability: not null -> nullable

### Type differences

#### account_state
- `critical` `gate` `enum_value_removed`: enum value "pending" exists only in development

### Partition changes

#### events_2024
- `high` `partition_changed`: partition: {bound=strategy=r from=('2024-01-01') to=('2025-01-01'), parent=events} -> {bound=strategy=r from=('2024-02-01') to=('2025-01-01'), parent=events}

### View dependency impact

- `active_user_emails` (transitive): depends on changed table(s) `users`
- `active_users` (direct): depends on changed table(s) `users`


## development -> production

- differences: 7 (critical 2, high 4, medium 1, low 0)
- incompatible (code generated from development against production): 3

### Table and column differences

#### users
- `high` `foreign_key_changed`: removed in production: foreign_key(columns=[org_id], references=orgs(id))
- `high` `unique_changed`: removed in production: unique(name=users_email_uq, columns=[email])
- `medium` `default_changed` column `email`: default: 'dev@example.com' -> 'prod@example.com'
- `high` `gate` `nullability_changed` column `email`: nullability: not null -> nullable

### Type differences

#### account_state
- `critical` `gate` `enum_value_removed`: enum value "pending" exists only in development

#### users
- `critical` `gate` `type_changed` column `id`: type: bigint -> integer

### Partition changes

#### events_2024
- `high` `partition_changed`: partition: {bound=strategy=r from=('2024-01-01') to=('2025-01-01'), parent=events} -> {bound=strategy=r from=('2024-06-01') to=('2025-01-01'), parent=events}

### View dependency impact

- `active_user_emails` (transitive): depends on changed table(s) `users`
- `active_users` (direct): depends on changed table(s) `users`


## staging -> production

- differences: 5 (critical 2, high 2, medium 1, low 0)
- incompatible (code generated from staging against production): 2

### Table and column differences

#### legacy
- `critical` `gate` `table_shape_changed`: no shared columns; staging=[c, d], production=[a, b]

#### users
- `high` `foreign_key_changed`: removed in production: foreign_key(columns=[org_id], references=orgs(id))
- `medium` `default_changed` column `email`: default: 'staging@example.com' -> 'prod@example.com'

### Type differences

#### users
- `critical` `gate` `type_changed` column `id`: type: bigint -> integer

### Partition changes

#### events_2024
- `high` `partition_changed`: partition: {bound=strategy=r from=('2024-02-01') to=('2025-01-01'), parent=events} -> {bound=strategy=r from=('2024-06-01') to=('2025-01-01'), parent=events}

### View dependency impact

- `active_user_emails` (transitive): depends on changed table(s) `users`
- `active_users` (direct): depends on changed table(s) `users`

