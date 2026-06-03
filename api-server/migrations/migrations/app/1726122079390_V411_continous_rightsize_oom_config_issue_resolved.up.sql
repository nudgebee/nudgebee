update
    auto_pilot
set
    rule = '{"min_cpu": 0.01, "min_memory": "100Mi", "min_change_threshold": 10, "cpu_analysis_strategy": "P90", "analysis_duration_hour": 24, "memory_analysis_strategy": "P90", "oom_kill_increase_factor": 1.4}'
where
    category = 'continuous_rightsize';
