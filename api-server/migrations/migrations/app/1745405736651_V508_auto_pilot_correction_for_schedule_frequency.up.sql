update auto_pilot set schedule_time = '50 * * * *' where category = 'horizontal_rightsize';
update auto_pilot set schedule_time = '0 * * * *' where category = 'pvc_rightsize';
update auto_pilot set schedule_time = '*/15 * * * *' where category = 'continuous_rightsize';
