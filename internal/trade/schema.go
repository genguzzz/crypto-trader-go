package trade

// Trading tables. Records are append-only except the state columns of accounts (day state,
// backtest progress), instances, orders and signal outcomes; trade_positions is a cache of
// current positions that can be rebuilt from fills.
const schema = `
CREATE TABLE IF NOT EXISTS trade_accounts(id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, mode TEXT NOT NULL,
  limits TEXT NOT NULL, archived INTEGER NOT NULL DEFAULT 0, from_ts INTEGER NOT NULL DEFAULT 0, to_ts INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL, backtest TEXT NOT NULL DEFAULT '', day_key INTEGER NOT NULL DEFAULT 0,
  day_start REAL NOT NULL DEFAULT 0, day_deposits REAL NOT NULL DEFAULT 0, halted INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS trade_strategy_versions(id TEXT PRIMARY KEY, name TEXT NOT NULL, file TEXT NOT NULL,
  description TEXT NOT NULL, timeframe TEXT NOT NULL, params TEXT NOT NULL, universe TEXT NOT NULL, code TEXT NOT NULL,
  created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS trade_instances(id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER NOT NULL,
  version_id TEXT NOT NULL, name TEXT NOT NULL, params TEXT NOT NULL, universe TEXT NOT NULL, exec TEXT NOT NULL,
  state TEXT NOT NULL, state_reason TEXT NOT NULL DEFAULT '', config_version INTEGER NOT NULL, heartbeat INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS trade_instances_acct ON trade_instances(account_id);
CREATE TABLE IF NOT EXISTS trade_instance_configs(instance_id INTEGER NOT NULL, version INTEGER NOT NULL, version_id TEXT NOT NULL,
  params TEXT NOT NULL, universe TEXT NOT NULL, exec TEXT NOT NULL, created_at INTEGER NOT NULL, PRIMARY KEY(instance_id, version));
CREATE TABLE IF NOT EXISTS trade_instance_state(instance_id INTEGER PRIMARY KEY, data BLOB, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS trade_signals(id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER NOT NULL, instance_id INTEGER NOT NULL,
  inst_id TEXT NOT NULL, action TEXT NOT NULL, ts INTEGER NOT NULL, recv_ts INTEGER NOT NULL, outcome TEXT NOT NULL,
  outcome_reason TEXT NOT NULL DEFAULT '', ai_decision_id INTEGER NOT NULL DEFAULT 0, body TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS trade_signals_acct ON trade_signals(account_id, id);
CREATE TABLE IF NOT EXISTS trade_orders(id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER NOT NULL, instance_id INTEGER NOT NULL,
  signal_id INTEGER NOT NULL, inst_id TEXT NOT NULL, side TEXT NOT NULL, pos_side TEXT NOT NULL, type TEXT NOT NULL,
  qty REAL NOT NULL, price REAL NOT NULL, trigger_px REAL NOT NULL, reduce_only INTEGER NOT NULL, oco_group TEXT NOT NULL,
  leverage REAL NOT NULL, state TEXT NOT NULL, filled REAL NOT NULL, avg_px REAL NOT NULL, reason TEXT NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS trade_orders_acct ON trade_orders(account_id, id);
CREATE INDEX IF NOT EXISTS trade_orders_open ON trade_orders(state);
CREATE TABLE IF NOT EXISTS trade_fills(id INTEGER PRIMARY KEY AUTOINCREMENT, order_id INTEGER NOT NULL, account_id INTEGER NOT NULL,
  instance_id INTEGER NOT NULL, inst_id TEXT NOT NULL, side TEXT NOT NULL, pos_side TEXT NOT NULL, qty REAL NOT NULL, px REAL NOT NULL,
  ct_val REAL NOT NULL, fee REAL NOT NULL, slippage_bp REAL NOT NULL, realized_pnl REAL NOT NULL, liquidation INTEGER NOT NULL,
  reduce INTEGER NOT NULL, closes INTEGER NOT NULL, ts INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS trade_fills_acct ON trade_fills(account_id, id);
CREATE TABLE IF NOT EXISTS trade_positions(account_id INTEGER NOT NULL, instance_id INTEGER NOT NULL, inst_id TEXT NOT NULL,
  body TEXT NOT NULL, PRIMARY KEY(account_id, instance_id, inst_id)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS trade_ledger(id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER NOT NULL, kind TEXT NOT NULL,
  amount REAL NOT NULL, inst_id TEXT NOT NULL DEFAULT '', ref_id INTEGER NOT NULL DEFAULT 0, ts INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS trade_ledger_acct ON trade_ledger(account_id, id);
CREATE TABLE IF NOT EXISTS trade_equity(account_id INTEGER NOT NULL, ts INTEGER NOT NULL, equity REAL NOT NULL,
  PRIMARY KEY(account_id, ts)) WITHOUT ROWID;
`
