import * as echarts from 'echarts/core'
import { BarChart, LineChart } from 'echarts/charts'
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
  DataZoomComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

// 按需注册：看板只用折线（趋势）与条形（排行），
// 避免全量引入把首包撑大（全量 ~1MB，按需 ~300KB）。
echarts.use([
  LineChart,
  BarChart,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  DataZoomComponent,
  CanvasRenderer,
])

export default echarts
