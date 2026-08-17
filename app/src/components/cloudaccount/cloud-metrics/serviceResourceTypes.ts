// Maps a cloud service name to the resource `type` that carries its metrics, used to
// filter cloud_resourses when fetching for a live metrics query. Needed where one
// service_name spans several resource types (e.g. AmazonEC2 = instances + EBS volumes
// + ENIs): without the filter the query would include non-metric resources and pass a
// wrong resource_type, so the AWS backend's metric auto-detect returns nothing. The
// value is also the backend's Metrices key (e.g. serviceConfig.Metrices["compute-instance"]).
export const SERVICE_TO_RESOURCE_TYPES: Record<string, string> = {
  AmazonEC2: 'compute-instance',
};
