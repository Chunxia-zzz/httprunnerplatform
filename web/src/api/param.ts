import { req } from './client'
import type { ID, ParamDataset, ParamDatasetReq } from './types'

/**
 * 参数化数据集（M3 · ③-b）。
 *
 * 路由契约（internal/api/router.go registerParamRoutes）：
 *   GET    /projects/:id/datasets   列表
 *   POST   /projects/:id/datasets   新建
 *   GET    /datasets/:id            详情
 *   PUT    /datasets/:id            更新
 *   DELETE /datasets/:id            删除
 *   GET    /datasets/:id/csv        读回 CSV 文本
 */

export function listDatasets(projectId: ID): Promise<ParamDataset[]> {
  return req<ParamDataset[]>({ method: 'GET', url: `/projects/${projectId}/datasets` })
}

export function getDataset(id: ID): Promise<ParamDataset> {
  return req<ParamDataset>({ method: 'GET', url: `/datasets/${id}` })
}

export function createDataset(projectId: ID, data: ParamDatasetReq): Promise<ParamDataset> {
  return req<ParamDataset>({ method: 'POST', url: `/projects/${projectId}/datasets`, data })
}

export function updateDataset(id: ID, data: ParamDatasetReq): Promise<ParamDataset> {
  return req<ParamDataset>({ method: 'PUT', url: `/datasets/${id}`, data })
}

export function deleteDataset(id: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/datasets/${id}` })
}

/** 读回 CSV 数据集的文件文本，前端编辑 CSV 时回填。 */
export function datasetCsvText(id: ID): Promise<string> {
  return req<{ csv_text: string }>({ method: 'GET', url: `/datasets/${id}/csv` }).then((r) => r.csv_text)
}
