import type { InstrumentInfo, Row } from '../../api'
/** Everything a stat detail view may need; views must tolerate missing row/values. */
export interface DetailCtx { id: string; info: InstrumentInfo; row?: Row }
