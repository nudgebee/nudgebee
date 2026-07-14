"""Copy-library display names: curated identifiers render human copy, unmapped
keys fall back to the previous title-casing so an unknown key is never worse
than before."""

from notifications_server.copy_library import EVENT_DISPLAY_NAMES, RULE_DISPLAY_NAMES, display_name
from notifications_server.message_templates.slack.recommendation_nudge_digest import format_rule_name


def test_rule_names_map_to_curated_copy():
    assert display_name("pod_right_sizing") == "Workload rightsizing"
    assert display_name("unused_pvc") == "Unused volume"
    assert display_name("eks_cluster_upgrade") == "Cluster upgrade"
    assert display_name("image_scan") == "Vulnerable image"
    assert display_name("k8s-cis-1.23") == "CIS benchmark failure"


def test_rule_name_variants_share_copy():
    # hyphenated / adapter spellings emitted by reports and account adapters
    assert display_name("replica-rightsizing") == display_name("replica_right_sizing")
    assert display_name("abandoned-resources") == display_name("abandoned_resource")
    assert display_name("rightsize_pvc") == display_name("pv_rightsize")


def test_event_keys_map_to_curated_copy():
    assert display_name("report_crash_loop") == "Crash loop"
    assert display_name("KubePodCrashLooping") == "Crash loop"
    assert display_name("pod_oom_killer_enricher") == "Out of memory (OOM-killed)"
    assert display_name("CPUThrottlingHigh") == "CPU throttling"
    assert display_name("Kubernetes Warning Event") == "Warning events"


def test_unmapped_key_falls_back_to_title_case():
    assert display_name("some_future_rule") == "Some Future Rule"
    assert display_name("Made-Up-Key") == "Made Up Key"


def test_empty_key_is_safe():
    assert display_name("") == ""
    assert display_name(None) == ""


def test_no_display_name_leaks_raw_separators():
    for name in list(RULE_DISPLAY_NAMES.values()) + list(EVENT_DISPLAY_NAMES.values()):
        assert "_" not in name, name


def test_format_rule_name_delegates_to_library():
    assert format_rule_name("pod_right_sizing") == "Workload rightsizing"
    assert format_rule_name("unknown_rule") == "Unknown Rule"
