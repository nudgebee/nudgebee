update recommendation r set account_object_id = CONCAT((cr.meta ->> 'namespace' :: text),'/',(cr.meta ->> 'controllerKind' :: text), '/', (cr.meta ->> 'controller' :: text)  )
from cloud_resourses cr where r.account_object_id is null  and  r.resource_id = cr.id and rule_name = 'pod_right_sizing';

update recommendation set category = 'RightSizing' where category = 'K8sPersistentVolumeRecommendation'; 

update recommendation set category = 'Configuration' where category = 'SSLCheck';