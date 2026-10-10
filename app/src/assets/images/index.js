// Re-exports only — enforced by scripts/check-assets-barrel.js (in `npm run lint2`).
// A `require()` or any other statement here stops the bundler tree-shaking this
// file, and every page then downloads every image below.
//   export * as Name from '…'            → the `{ default }` module (use Name.default, or SafeIcon)
//   export { default as Name } from '…'  → the SVGR component itself (use <Name />)

/***
 * @authentication
 */
export * as logoSvg from '@assets/authentication/logo.svg';
export * as backgroundAuth from '@assets/authentication/bg1.svg';
export * as validationError from '@assets/authentication/error.svg';
export * as validationSuccess from '@assets/authentication/success.svg';

export * as image_1296 from '@assets/authentication/image-1296.png';
export * as image_1314 from '@assets/authentication/image-1314.png';
export * as image_1293 from '@assets/authentication/image-1293.png';
export * as image_1212 from '@assets/authentication/image-1212.png';
export * as modalerror from '@assets/authentication/error-big.svg';
export * as modalPasswordChange from '@assets/authentication/security.svg';
export * as googleAuth from '@assets/authentication/google.svg';
export * as oktaAuth from '@assets/authentication/okta.svg';
export * as oneloginAuth from '@assets/authentication/one-login.svg';
export * as azureAuth from '@assets/authentication/azure.svg';
export * as auth0Auth from '@assets/authentication/auth0.svg';

/***
 * @header
 */
export * as temp1 from '@assets/header/temp1.svg';
export * as expandMore from '@assets/header/expand_more.svg';
export * as logoutSvg from '@assets/header/logout.svg';
export * as modalSuccess from '@assets/header/check.svg';

export * as NotificationIcon from '@assets/header/notification.svg';
export * as DocumentationIcon from '@assets/header/Documentation.svg';
export * as DocumentationIconBlue from '@assets/header/Documentation-blue.svg';
export * as SettingsIcon from '@assets/header/Settings.svg';
export * as UserIcon from '@assets/header/User.svg';
export * as HelpOutlineDarkIcon from '@assets/new/help-circle-dark.svg';
export * as NotificationOutlineIconDark from '@assets/new/bell-icon-dark.svg';
export * as ChatOutlineDarkIcon from '@assets/new/chat-dark-icon.svg';

export { default as AdminIconBlue } from '@assets/header/AdminIconBlue.icon.svg';
export { default as AutopilotIconBlue } from '@assets/header/AutopilotIconBlue.icon.svg';
export { default as ClusterIconBlue } from '@assets/header/ClusterIconBlue.icon.svg';
export { default as AgentIconBlue } from '@assets/header/agent_icon_blue.icon.svg';
export { default as HomeIconBlue } from '@assets/header/HomeIconBlue.icon.svg';
export { default as SummaryIconBlue } from '@assets/header/SummaryIconBlue.icon.svg';
export { default as OptimiseIconBlue } from '@assets/header/OptimiseIconBlue.icon.svg';
export { default as TicketIconBlue } from '@assets/header/TicketIconBlue.icon.svg';
export * as TroubleshootIconBlue from '@assets/header/TroubleshootIconBlue.icon.svg';
export * as SecurityIcon from '@assets/topnav/security-verified-icon.svg';
export * as TroubleshootIcongrey from '@assets/header/TroubleshootIcongrey.svg';
export * as AppsInfra from '@assets/header/Apps-Infra.svg';
export { default as AppsInfraBlue } from '@assets/header/AppsInfraIconBlue.icon.svg';
export * as Monitoringgrey from '@assets/header/Monitoring-grey.svg';
export { default as MonitoringIconBlue } from '@assets/header/MonitoringIconBlue.icon.svg';
export * as Securitytools from '@assets/header/Security-Tools.svg';
export { default as SecuritytoolsBlue } from '@assets/header/SecurityToolsIconBlue.icon.svg';
export * as DocumentVerifiedIcon from '@assets/topnav/document-verified-icon.svg';
export * as GroupingIcon from '@assets/header/group-icon.svg';
export * as GrafanaIcon from '@assets/header/grafana-icon.svg';

/***
 * @sidebar
 */
export { default as homeIcon1 } from '@assets/sidebar-icon/home.icon.svg';
export { default as autopilotIcon1 } from '@assets/sidebar-icon/autopilot.icon.svg';
export { default as dashboardIcon1 } from '@assets/sidebar-icon/dashboard.icon.svg';
export { default as kubernetesIcon1 } from '@assets/sidebar-icon/kubernetes.icon.svg';
export { default as ticketsIcon1 } from '@assets/sidebar-icon/tickets.icon.svg';
export * as ticketIconBlack from '@assets/sidebar-icon/ticket-icon2.svg';
export { default as troubleshootIcon1 } from '@assets/sidebar-icon/troubleshoot.icon.svg';
export * as troubleshootIconBlack from '@assets/sidebar-icon/troubleshootBlack.svg';
export * as GroupOfUsersIcon from '@assets/group-users.svg';

export * as CloudAccountIcon from '@assets/sidebar-icon/cloud-account.svg';
export { default as VmIcon } from '@assets/sidebar-icon/vm.icon.svg';
export * as VmServerIcon from '@assets/new/vm-server.svg';
export * as CloudIconBlackOutline from '@assets/sidebar-icon/cloud-black-outline.svg';
export * as ToolIcon from '@assets/Tool.svg';
export { default as ToolIconBlue } from '@assets/kubernetes/tool-icon-blue.icon.svg';
export * as AutoPilotIcon from '@assets/sidebar/Autopilot.svg';
export * as AutoPilotGreyIcon from '@assets/sidebar/Autopilot_row.svg';
export * as AutoPilotHightlightIcon from '@assets/sidebar/Autopilot_green.svg';
export * as AdminIcon from '@assets/sidebar/Admin.svg';
export * as OptimizeIcon from '@assets/sidebar/Optimize.svg';
export * as OptimizeIconBlack from '@assets/optimize-icon.svg';
export { default as WrenchIconOutline } from '@assets/wrench-outline.icon.svg';
export * as OptimizeIconBlue from '@assets/header/optimize-blue.icon.svg';

export * as AutoPilotIcon1 from '@assets/application/auto-pilot-selected.svg';
export * as AutoPilotIconBlack from '@assets/application/auto-pilot-new.svg';

export { default as WhiteOptimizeIcon } from '@assets/header/whiteOptimizeIcon.icon.svg';

/***
 * @user
 */
export * as moreItems from '@assets/user-management/more_vert_dark.svg';
export * as addCircle from '@assets/user-management/add_circle.svg';
export * as downloadIcon from '@assets/user-management/download-icon.svg';
export * as downloadIconYellow from '@assets/user-management/download-icon.svg';
export * as lockUser from '@assets/user-management/lock-user.svg';
export * as lockUser1 from '@assets/user-management/lock-user1.svg';
export * as disabledUser from '@assets/user-management/disabled-user.svg';
export * as Gshow from '@assets/user-management/add_circle.svg';
export * as ApprovalPolicyIcon from '@assets/user-management/Approval-Policy.svg';
export { default as AuditIcon } from '@assets/user-management/Audit.icon.svg';
export { default as BillingIcon } from '@assets/user-management/Billing.icon.svg';
export { default as IntegrationsIcon } from '@assets/user-management/Integrations.icon.svg';
export { default as User1 } from '@assets/user-management/User.icon.svg';
export { default as UserGroupIcon } from '@assets/user-management/User-Group.icon.svg';
export { default as NotificationIcon1 } from '@assets/user-management/Notification.icon.svg';

/***
 * @OUmanagement
 */
export * as menuShow from '@assets/ou-management/g_show.svg';
export * as menuUser from '@assets/ou-management/g_user.svg';
export * as menuOU from '@assets/ou-management/g_OU.svg';
export * as archiveImg from '@assets/ou-management/archive.svg';
export * as menuBuildBlack from '@assets/ou-management/g_build_black.svg';
export * as addFolder from '@assets/ou-management/add_folder_black.svg';
export * as menuAccount from '@assets/ou-management/g_account_tree.svg';
export * as menuDownload from '@assets/ou-management/g_download_project.svg';
export * as menuMove from '@assets/ou-management/g_move.svg';
export * as menuCloud from '@assets/ou-management/g_cloud.svg';

export * as ouStarImg from '@assets/ou-management/star.svg';
export * as moveVertDark from '@assets/ou-management/more_vert_dark.svg';
export * as ouAws from '@assets/ou-management/c_aws.svg';
export { default as AWSIcon } from '@assets/ou-management/aws-icon.icon.svg';
export { default as ouK8s } from '@assets/ou-management/kubernetes_icon.icon.svg';
export { default as ouAzure } from '@assets/ou-management/c_asure.icon.svg';
export { default as ouGoogle } from '@assets/ou-management/c_google.icon.svg';
export * as ouSnowFlake from '@assets/ou-management/SNOW.svg';
export * as ouOpenAi from '@assets/ou-management/openai.svg';
export * as ouSnowflake from '@assets/ou-management/snowflake.png';
export * as ouRelic from '@assets/ou-management/new_relic.svg';
export * as ouProjects from '@assets/ou-management/ic_projects.svg';
export * as ouPostgres from '@assets/ou-management/postgres_icon.svg';
export * as ouMssql from '@assets/sql-icon.svg';
export * as ouOracle from '@assets/oracle-icon.svg';
export * as ouMsTeams from '@assets/ou-management/ms_teams.icon.svg';

export * as searchSvg from '@assets/ou-management/search.svg';
export * as downloadSvg from '@assets/ou-management/download.svg';
export * as resetSvg from '@assets/ou-management/reset.svg';

export * as starBlackBorder from '@assets/ou-management/star_border_black.svg';
export * as ouNoProjects from '@assets/ou-management/no-project.svg';
/***
 * @ProjectManagement
 */
export * as unLoackIcon from '@assets/ou-management/unlock-cloud.svg';
export * as cloudBlackIcon from '@assets/ou-management/cloud_black.svg';
export * as ArrowBarICon from '@assets/ou-management/arrow_drop_down_black.svg';
export * as equalICon from '@assets/ou-management/equal.svg';
export * as closeICon from '@assets/ou-management/close.svg';

export * as accList1 from '@assets/accounts/list1.svg';
export * as accChevronRight from '@assets/accounts/chevron_right.svg';
export * as accFileDownload from '@assets/accounts/file_download_black.svg';
export * as accSettingsBlack from '@assets/accounts/settings_black.svg';
export * as accMoreVertDark from '@assets/accounts/more_vert_dark.svg';

export * as finCopy1 from '@assets/financials/copy1.svg';
export * as finAlert from '@assets/financials/alert.svg';
export * as finIcProjects2 from '@assets/financials/ic_projects_2.png';
export * as finNounSecureDollar from '@assets/financials/noun-secure-dollar.svg';
export * as finInfoBlack from '@assets/financials/info_black2.svg';
export { default as DollarIcon } from '@assets/financials/dollar-icon.icon.svg';

export * as aapDownArrow from '@assets/administrative_appeal/down_arrow2.svg';
export * as aapRightArrow from '@assets/administrative_appeal/right_arrow3.svg';
export * as aapLocationOnBlack from '@assets/administrative_appeal/location_on_black.svg';
export * as aapOrganizationChart from '@assets/administrative_appeal/organization-chart.png';
export * as aapSettings from '@assets/administrative_appeal/setting2.svg';
export * as aapPublicBlack from '@assets/administrative_appeal/public_black.svg';
export * as aapCheckCircle from '@assets/administrative_appeal/down_arrow2.svg';

export * as pmCheckCircle from '@assets/project_management/check-circle.svg';

export * as icOU from '@assets/ic/ic_ou.svg';
export * as timerSVG from '@assets/timer.svg';
export * as bellSVG from '@assets/bell2.svg';
export * as jiraIcon from '@assets/jira_icon.icon.svg';
export * as ConfluenceIcon from '@assets/confluence-icon.svg';
export * as serviceNowIcon from '@assets/servicenow.icon.svg';
export * as freshdeskIcon from '@assets/freshdesk.icon.svg';
export * as slackIcon from '@assets/slack_icon.icon.svg';
export * as GithubIcon from '@assets/github-icon.icon.svg';
export * as GitMergeIcon from '@assets/git-merge.svg';
export * as GChatIcon from '@assets/gchat-icon.icon.svg';
export * as DiscordIcon from '@assets/discord-icon.icon.svg';
export * as downArrowSVG from '@assets/down_arrow3.svg';
export * as downArrow3SVG from '@assets/down_arrow3.svg';
export * as downArrow4SVG from '@assets/down_arrow4.svg';
export * as downArrowRed from '@assets/red_arrow_down.svg';
export * as upArrowGreen from '@assets/green_arrow_up.svg';
export * as shareBlackSVG from '@assets/share_black1.svg';
export * as info2SVG from '@assets/info_2.svg';
export * as setting2SVG from '@assets/setting2.svg';
export * as publicBlackSVG from '@assets/public_black.svg';
export * as calanderBlackSVG from '@assets/calendar_black.svg';
export * as timelapseBlackSVG from '@assets/timelapse_black.svg';
export * as timelapse from '@assets/timelapse-icon.svg';
export * as scheduleBlackSVG from '@assets/schedule_black.svg';
export * as uploadIcon from '@assets/upload_dummy.svg';
export * as userIcon from '@assets/ou-management/g_user.svg';
export * as error404Image from '@assets/404_image.svg';
export * as workflowLoadergif from '@assets/workflowLoader.gif';

export * as copyRightImg from '@assets/financials/copy1.svg';
export * as ruleSystemImg from '@assets/financials/copy1.svg';
export * as calenderDataIcon from '@assets/calendar_today.svg';

export * as mfilterSvg from '@assets/m_filter.svg';
export * as mshowSvg from '@assets/m_show.svg';
export * as mblockSvg from '@assets/m_block.svg';
export * as maddUserSvg from '@assets/m_adduser.svg';
export * as mloginSvg from '@assets/m_login.svg';
export * as successCheckSvg from '@assets/check.svg';
export * as checkIconBold from '@assets/check-icon-bold.svg';
export * as checkFilledIcon from '@assets/check-filled-icon.svg';
export * as attachSvg from '@assets/attach.svg';
export * as treeSvg from '@assets/tree.svg';
export * as tree1Svg from '@assets/tree1.svg';
export * as tree2Svg from '@assets/tree2.svg';
export * as calendarViewWeek from '@assets/calendarViewWeek.svg';
export * as assignmentBlackSvg from '@assets/assignment_black.svg';
export * as monetizationOnBlackSvg from '@assets/monetization_on_black.svg';

export * as icProjectsFillSvg from '@assets/ic/ic_projects_fill.svg';
export * as earlyAccess from '@assets/earlyAccess.svg';
export * as infoIcon from '@assets/info_icon.svg';

export * as addAccount from '@assets/new/plus.png';
export * as plusIcon from '@assets/m_add_black.svg';
export * as listAccount from '@assets/new/list.png';
export * as upArrowRed from '@assets/new/redArrowUp.png';
export * as downArrowGreen from '@assets/new/GreenArrowDown.png';

export * as newLogo from '@assets/logo/logo.svg';
export * as logoText from '@assets/logo/logo_text.svg';
export * as logoTextPrimary from '@assets/logo/logo_text_primary.svg';
export * as location from '@assets/location-icon.svg';
export * as electricBolt from '@assets/electricbolt-icon.svg';

export * as newAwsLogo from '@assets/logo/aws_logo.png';
export * as gcloudLogo from '@assets/logo/gcloud.svg';

export * as writeIconLight from '@assets/new/write-icon-light.svg';
export * as ArrowDown from '@assets/arrow_down.svg';

export * as addIcon from '@assets/ou-management/plus-circle.svg';
export * as addIconWhite from '@assets/ou-management/plus-circle-white.svg';
export * as fourSquaresIcon from '@assets/ou-management/four-square-icon.svg';
export * as peopleGroupIcon from '@assets/ou-management/people-group-icon.svg';

export * as TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';

export * as CloudUploadIcon from '@assets/cloud-upload.svg';

export * as K8EventsIcon from '@assets/auto-pilot/k8-events.svg';
export * as K8EventsIconSelected from '@assets/auto-pilot/k8-events-selected.svg';
export * as ScheduleIcon from '@assets/auto-pilot/schedule.svg';
export * as ScheduleIconSelected from '@assets/auto-pilot/schedule-selected.svg';
export * as LogEventsIcon from '@assets/auto-pilot/log-events.svg';
export * as LogEventsIconSelected from '@assets/auto-pilot/log-events-selected.svg';
export * as PromQLIcon from '@assets/auto-pilot/promql.svg';
export * as PromQLIconSelected from '@assets/auto-pilot/promql-selected.svg';
export * as NotifyIcon from '@assets/auto-pilot/notify.svg';
export * as CallApiIcon from '@assets/auto-pilot/call-api.svg';
export * as ExecuteCliIcon from '@assets/auto-pilot/execute-cli.svg';
export * as PvPvcIcon from '@assets/auto-pilot/pv-pvc.svg';
export * as ScaleCpuIcon from '@assets/auto-pilot/scale-cpu.svg';
export * as ScaleReplicasIcon from '@assets/auto-pilot/scale-replicas.svg';
export * as CreateTicketIcon from '@assets/auto-pilot/create-ticket.svg';
export * as EmailIcon from '@assets/auto-pilot/email.svg';
export * as JiraIcon from '@assets/auto-pilot/jira.svg';
export * as PagerDutyIcon from '@assets/auto-pilot/pager-duty.svg';
export * as ZenDutyIcon from '@assets/zenduty.jpeg';
export * as SlackIcon from '@assets/auto-pilot/slack.svg';
export * as MSTeamsIcon from '@assets/ms_teams_s.svg';
export * as ServiceNowIcon from '@assets/auto-pilot/service-now.svg';
export * as FreshdeskIcon from '@assets/freshdesk.icon.svg';
export * as ScaleCpuNewIcon from '@assets/new/scale-cpu-icon.svg';

export * as TroubleshootIcon from '@assets/auto-pilot/k8-events.svg';
export * as TroubleshootIconSelected from '@assets/auto-pilot/k8-events-selected.svg';

export * as DashboardIcon from '@assets/home/Dashboard_Icon.svg';
export * as CertificateIcon from '@assets/troubleshoot/CertificateIcon.svg';
export * as CertificateGreenIcon from '@assets/troubleshoot/verified-green.svg';
export * as BarsBlueOutlineIcon from '@assets/troubleshoot/bars-outline.svg';
export * as ErrorFillIcon from '@assets/troubleshoot/error-fill.svg';
export * as LastStateIcon from '@assets/troubleshoot/last-state-icon.svg';
export * as FileOutlineIcon from '@assets/troubleshoot/file-outline.svg';
export * as SparklesIconBG from '@assets/troubleshoot/sparkles-bg.svg';
export * as GraphOutlineIcon from '@assets/troubleshoot/graph-up.svg';
/** @deprecated Use WorkflowIconBlue instead */
export * as WorkflowIconBlue from '@assets/workflow/workflow-icon-blue.icon.svg';
export * as WorkflowIconWhite from '@assets/workflow/workflow-icon-white.icon.svg';

export * as NotificationOutlineIcon from '@assets/new/bell-outline-icon.svg';
export * as HelpOutlineIcon from '@assets/new/help-circle-outline.svg';
export { default as ProfileOutlineIcon } from '@assets/new/profile-outline-icon.icon.svg';
export * as SettingOutlineIcon from '@assets/new/settings-outline-icon.svg';
export * as SettingOutlineIconGrey from '@assets/new/settings-outline-icon-grey.svg';
export * as TracesIcon from '@assets/anchorcomponent/Traces.svg';
export * as RightArrowIcon from '@assets/arrow-square-right.png';

export * as StashIcon from '@assets/stash-icon.svg';
export * as SwitchTenentIconDark from '@assets/new/switch-accounts-dark.svg';
export * as LogoutIconDark from '@assets/new/log-out-dark.svg';

// Monitoring
export { default as AlertTriangleIcon } from '@assets/monitoring/alert-triangle.icon.svg';
export { default as CPUIcon } from '@assets/monitoring/cpu.icon.svg';
export { default as FlashIcon } from '@assets/monitoring/flash.icon.svg';
export { default as LatencyClockIcon } from '@assets/monitoring/latency-clock.icon.svg';
export { default as MemoryCardIcon } from '@assets/monitoring/memory-card.icon.svg';
export { default as RocketIcon } from '@assets/monitoring/rocket.icon.svg';
export { default as SLAClockIcon } from '@assets/monitoring/sla-clock.icon.svg';
export { default as BetaIcon } from '@assets/beta-icon.icon.svg';
export * as AlphaIcon from '@assets/alpha-icon.svg';

// signin
export * as WelcomeLogo from '@assets/welcome-logo.svg';
export * as WelcomeSlideOne from '@assets/signin-slide-one.svg';
export * as WelcomeSlide from '@assets/signin-new.png';
export * as LogoWithText from '@assets/NB-logo-with-text.svg';
export * as WelcomeSlideTwo from '@assets/signin-slide-two.svg';
export * as NudgeBeeDarkTextLogo from '@assets/Nudgee-text-logo-dark.svg';
export * as StarsIcon from '@assets/stars.svg';
export * as NBIconSignIn from '@assets/nb-logo-signin.svg';
export * as SignInInvestigate from '@assets/sign-in-investigate.png';
export * as SignInTroubleshoot from '@assets/sign-in-troubleshooting.png';
export * as SignInWorkflow from '@assets/sign-in-workflow.png';
export * as SignInOptimize from '@assets/sign-in-optimize.png';
export * as NbIconLight from '@assets/Logo-Icon-transparent-Yellow.svg';

// welcome modal
export * as WelcomeTextLogo from '@assets/welcome-text-logo.svg';
export * as WelcomeRightImage from '@assets/welcome-right.svg';
export * as ExternalLinkIcon from '@assets/external-link-icon.svg';
export * as LogoIcon from '@assets/logo.svg';

export * as SLOInspectionWhiteIcon from '@assets/kubernetes/slo-inspection-white.svg';
export * as SLOInspectionBlackIcon from '@assets/kubernetes/slo-inspection.svg';

export * as EmailIconBlack from '@assets/kubernetes/email-icon.svg';
export * as EmailIconWhite from '@assets/kubernetes/email-icon-white.svg';

export * as ScaleIcon from '@assets/application/scale-new.svg';

//
export * as ThumbsUp from '@assets/Thumb.svg';

// OPTIMIZE
export * as RefreshIcon from '@assets/refresh.svg';

//Auto-Pilot
export * as SkipForwardIcon from '@assets/auto-pilot/skip-forward-icon.svg';
export * as ManualRunIcon from '@assets/auto-pilot/manual-run-icon.svg';

// HOME page
export * as CpuRedIcon from '@assets/home/new/cpu.svg';
export * as InfoRedIcon from '@assets/home/new/info-red.svg';
export * as MemoryRedIcon from '@assets/home/new/memory.svg';
export * as WrenchIcon from '@assets/home/new/wrench.svg';
export * as QuickLink from '@assets/home/new/quick-link.icon.svg';
export * as MetricsBlueIcon from '@assets/home/new/matrics-blue.svg';
export * as NewExpandIcon from '@assets/expand-new-icon.icon.svg';
export * as DataBaseDark from '@assets/home/new/Database2.svg';
export * as QueueDark from '@assets/home/new/Queues.svg';
export * as FeedbackBlueIcon from '@assets/home/new/feedback-blue.svg';
export * as MessageBlueIcon from '@assets/home/new/message-blue.svg';
export * as RepoBlueIcon from '@assets/home/new/repo-forked-blue.icon.svg';
export * as SirenBlueIcon from '@assets/home/new/siren-rounded-blue.icon.svg';
export * as TicketBlueIcon from '@assets/home/new/ticket-blue.icon.svg';
export * as YellowRightArrowIcon from '@assets/home/new/yellow-right-icon.svg';
export * as PagerDutyNewIcon from '@assets/home/new/pager-duty-new-logo.icon.svg';
export * as InMemoryIcon from '@assets/home/new/in-memory-icon.svg';

export * as DataNotAvailable from '@assets/Icon-no-data-available.svg';

export { default as FullScreenIcon } from '@assets/fullscreen.icon.svg';

export * as FilterIcon from '@assets/filter.svg';
export * as CopyIcon from '@assets/copy-icon.svg';
export * as CopyIconBlue from '@assets/copy-icon-blue.svg';
export * as DoubleArrowRight from '@assets/green-arrow-right.svg';
export * as OptimizeIconHome from '@assets/home/optimize-icon-home.icon.svg';
export * as K8sOpsHomeIcon from '@assets/home/k8s-ops-icon.icon.svg';
export * as InvestigateHomeIcon from '@assets/home/Investigate-button-icon.icon.svg';

export * as EmptyInsightIcon from '@assets/empty-insight.svg';
export * as RecentErrorIcon from '@assets/home/new/recent-error.icon.svg';
export * as MatricsIcon from '@assets/home/new/metrics_icon.icon.svg';
export * as MetricsIconBlack from '@assets/home/new/metrics-icon-black.svg';
export * as PodsIcon from '@assets/home/new/pods_icon.icon.svg';
export * as ServiceMapsIcon from '@assets/home/new/service_maps_icon.icon.svg';
export * as IncomingIcon from '@assets/new/Incoming.svg';
export * as OutgoingIcon from '@assets/new/Outgoing.svg';
export * as AskNudgebeeSuccessIcon from '@assets/ask-nudgebee/success-icon.svg';
export * as AskNudgebeeSkipIcon from '@assets/ask-nudgebee/skip-icon.svg';
export * as AskNudgebeeErrorIcon from '@assets/ask-nudgebee/error-icon.svg';
export * as AskNudgebeeLikeIcon from '@assets/ask-nudgebee/thumbs-up.svg';
export * as AskNudgebeeInProgressIcon from '@assets/ask-nudgebee/inprogress.svg';
export * as AskNudgebeeWaitingIcon from '@assets/ask-nudgebee/waiting-icon.svg';
export * as AskNudgebeeDislikeIcon from '@assets/ask-nudgebee/thumbs-down.svg';
export * as WatchIcon from '@assets/ask-nudgebee/watch-icon.svg';
export * as UsersIcon from '@assets/ask-nudgebee/Users.svg';
export * as ChatRoundedIcon from '@assets/ask-nudgebee/chat-round-icon.svg';
export * as FinalResponseIcon from '@assets/ask-nudgebee/final-reponse-text.svg';

// grouping
export * as PlusIcon from '@assets/PlusIcon.svg';
export * as EditIcon from '@assets/application/edit-new.svg';
export * as CrossIcon from '@assets/cross-icon.svg';
export * as ServiceMapsIconBlack from '@assets/home/new/service_maps_icon_black.svg';

// autopilot
export * as DadicatedPodIcon from '@assets/new/pod-icon.svg';
export * as EphemeralContainerIcon from '@assets/new/container-volume-logo.svg';
export * as RightSiingNewIcon from '@assets/new/right-sizing-new-icon.svg';
export * as ArrowDownNew from '@assets/new/arrow-down-new.svg';
export * as ArrowUpNew from '@assets/new/arrow-up-new.svg';
export * as CopyNewIcon from '@assets/new/copy-new-icon.svg';
export * as TerminalIcon from '@assets/new/terminal-browser.svg';
export * as PlusBlueIcon from '@assets/plus-blue-icon.svg';
export * as ArrowRightBlueIcon from '@assets/arrow-right-blue.svg';
export * as RunIcon from '@assets/auto-pilot/manual-run.svg';
export * as RunSelectedIcon from '@assets/auto-pilot/manual-run-selected.svg';
export * as ApiIcon from '@assets/auto-pilot/api-icon.svg';

// resolution
export * as PrOpenIcon from '@assets/new/pr-open-icon.svg';
export * as UpdateIcon from '@assets/new/update-icon.svg';
export * as InvestigationOutlineIcon from '@assets/new/Investigation-outline-icon.svg';
export * as SideBarLockIcon from '@assets/new/sidebar-icon.svg';
export * as SqlIcon from '@assets/sql-icon.svg';
export * as PrometheusIcon from '@assets/prometheus-icon.svg';
export * as PostgresIcon from '@assets/postgres-icon.svg';
export * as ClickhouseIcon from '@assets/clickhouse-icon.svg';
export * as DatadogIcon from '@assets/datadog-icon.svg';
export * as DynatraceIcon from '@assets/dynatrace-icon.png';
export * as SignozIcon from '@assets/signoz-icon.svg';
export * as ArgocdIcon from '@assets/argocd.svg';
export * as ElasticSearchIcon from '@assets/elasticsearch-icon.svg';
export * as PinotIcon from '@assets/pinot-icon.svg';
export * as HiveIcon from '@assets/hive-icon.svg';
export { default as K8sIcon } from '@assets/kubernetes-icon.icon.svg';
export * as MetricsIcon from '@assets/metrics-icon.svg';
export * as EventIcon from '@assets/events-icon.svg';
export * as EventIconBlue from '@assets/events-icon-blue.svg';
export * as LokiIcon from '@assets/loki-icon.svg';
export * as ResponseIcon from '@assets/response-tick.svg';
export { default as ArrowRightWhiteIcon } from '@assets/arrow-right-white.icon.svg';
export * as HideShowIcon from '@assets/hide-show-icon.svg';
export * as SuccessIcon from '@assets/success-icon.svg';
export * as ErrorIcon from '@assets/error-icon.svg';
export * as RunningIcon from '@assets/running-icon.svg';
// Conversation status neutral-terminal icons (#36205). StoppedIcon = grey stop
// square for user-initiated TERMINATED. InterruptedIcon = amber dash for
// supervisor-reap KILLED (cut short, may need review — deliberately distinct
// from ErrorIcon so users don't confuse it with a failure). QueuedIcon =
// lighter-blue dashed circle for PENDING (queued, not yet running — visually
// distinct from RunningIcon which is the active-in-progress state).
export * as StoppedIcon from '@assets/stopped-icon.svg';
export * as InterruptedIcon from '@assets/interrupted-icon.svg';
export * as QueuedIcon from '@assets/queued-icon.svg';
// Neutral gray dot for unmapped backend statuses — SafeIcon renders <img src>
// so a wrapper CSS color is a no-op; the fallback icon itself must be gray.
export * as UnknownIcon from '@assets/unknown-icon.svg';
export * as ThumbsUpOutlineIcon from '@assets/thumbs-up-outline.svg';
export * as ThumbsDownOutlineIcon from '@assets/thumbs-down-outline.svg';
export * as ExpandIcon from '@assets/expand-icon.svg';
export * as checklistIcon from '@assets/checklist-icon.svg';
export * as ManualTriggerIconBlue from '@assets/new/manual-icon-blue.svg';
export * as AutomateBlue from '@assets/new/automate-blue.svg';
export * as TimelineIcon from '@assets/kubernetes/timeline-icon.svg';

export * as attrCpuIcon from '@assets/home/attr-cpu-icon.svg';
export * as attrApiIcon from '@assets/home/new_home_icons/API-with-BG.svg';
export * as attrAppErrorIcon from '@assets/home/new_home_icons/App-error-with-BG.icon.svg';
export * as attrConfigureIcon from '@assets/home/new_home_icons/Configure-with-BG.icon.svg';
export * as attrCpuNewIcon from '@assets/home/new_home_icons/CPU-with-BG.icon.svg';
export * as attrDiscIcon from '@assets/home/new_home_icons/Disk-with-BG.icon.svg';
export * as attrErrorIcon from '@assets/home/new_home_icons/Error-with-BG.icon.svg';
export * as attrCertificateExpireIcon from '@assets/home/new_home_icons/error-16-svgrepo-com2.icon.svg';
export * as attrCVEIcon from '@assets/home/new_home_icons/Images-with-BG.icon.svg';
export * as attrNetworkIcon from '@assets/home/new_home_icons/Network-with-BG.svg';
export * as attrPodIcon from '@assets/home/new_home_icons/Pod-with-BG.icon.svg';
export * as attrPVIcon from '@assets/home/new_home_icons/PV-with-BG.svg';
export * as attrPVIconGreen from '@assets/home/new_home_icons/PV-with-BG-Green.icon.svg';
export * as attrReplicaIcon from '@assets/home/new_home_icons/Replica-with-BG.svg';
export * as attrRightSizingIcon from '@assets/home/new_home_icons/Right-size-with-BG.icon.svg';
export * as attrRunIcon from '@assets/home/new_home_icons/Run-with-BG.svg';
export * as attrServiceIcon from '@assets/home/new_home_icons/Service-with-BG.icon.svg';
export * as attrVersionIcon from '@assets/home/new_home_icons/Version-with-BG.svg';
export * as attrFixIcon from '@assets/home/new_home_icons/Fix-button-icon.icon.svg';
export * as attrAppIcongreen from '@assets/home/new_home_icons/App-Icon-Green.icon.svg';

export * as RCAAnalysisIcon from '@assets/kubernetes/rcaanalysis-icon.svg';

// ask nudgebee
export * as ArrowBackGrayIcon from '@assets/arrow/arrow-back-gray.svg';
export * as ArrowBackGrayIconDark from '@assets/arrow/arrow-back-gray-dark.svg';
export * as CollapseLeftIcon from '@assets/arrow/collapse-icon.svg';
export * as StarOutline from '@assets/ask-nudgebee/star-outline.svg';
export * as StarSelected from '@assets/ask-nudgebee/star-selected.svg';
export * as RecommendationIcon from '@assets/kubernetes/recommendations-icon.svg';
export * as SaveIconOutline from '@assets/ask-nudgebee/Save-outline.svg';
export * as SaveIconOutlineGrey from '@assets/new/save-outline-icon-grey.svg';
export * as SaveIconOutlinelight from '@assets/ask-nudgebee/Save-outline-light.svg';
export * as SaveIconOutlineselect from '@assets/ask-nudgebee/Save-outline-selected.svg';
export * as UserIconOutline from '@assets/ask-nudgebee/User-outline.svg';
export * as DeleteIconRed from '@assets/ask-nudgebee/DeleteRed.svg';
export * as ShareIconBlue from '@assets/ask-nudgebee/ShareBlue.svg';
export * as CPUIconRed from '@assets/ask-nudgebee/new/CPU-red.svg';
export * as CPUIconGreen from '@assets/ask-nudgebee/new/CPU-green.svg';
export * as DatabaseIconGreen from '@assets/ask-nudgebee/new/database-green.svg';
export * as InfoIconRed from '@assets/ask-nudgebee/new/info-red-icon.svg';
export * as PlayIconBlue from '@assets/ask-nudgebee/new/play-blue-icon.svg';
export * as ShieldIconBlue from '@assets/ask-nudgebee/new/shield-blue-icon.svg';
export * as PodCubeIconRed from '@assets/ask-nudgebee/new/pod-cube-red.svg';
export * as PlusIconSecondary from '@assets/ask-nudgebee/new/plus-icon.svg';
export * as RecentChatIcon from '@assets/ask-nudgebee/new/recent-chat-icon.svg';
export * as ListIcon from '@assets/ask-nudgebee/new/list-cross-icon.svg';
export * as ArrowRightYellowIcon from '@assets/ask-nudgebee/new/right-arrow-icon-yellow.svg';

// optimize dropdown icons
export { default as AbandonedAppsIcon } from '@assets/kubernetes/optimize-icons/abandoned-apps-icon.icon.svg';
export { default as BestPracticesIcon } from '@assets/kubernetes/optimize-icons/best-practices-icon.icon.svg';
export { default as PvcSightSizing } from '@assets/kubernetes/optimize-icons/pv-right-sizing.icon.svg';
export { default as ReplicaRightSizingIcon } from '@assets/kubernetes/optimize-icons/replica-right-sizing-icon.icon.svg';
export { default as RightSizingIcon } from '@assets/kubernetes/optimize-icons/right-sizing-icon.icon.svg';
export { default as SpotRecommendationIcon } from '@assets/kubernetes/optimize-icons/spot-recommendation-icon.icon.svg';
export { default as OptimizeSummaryIcon } from '@assets/kubernetes/optimize-icons/summary-icon.icon.svg';
export { default as UnusedVolumeIcon } from '@assets/kubernetes/optimize-icons/unused-volume-icon.icon.svg';
export { default as AutoScalerIcon } from '@assets/auto-scaler-icon.icon.svg';
export { default as RecommendationResolutionIcon } from '@assets/kubernetes/optimize-icons/recommendation-resolution.icon.svg';

// event dropdown icons
export { default as AllEventsIcon } from '@assets/kubernetes/event-icons/all-events-icon.icon.svg';
export { default as AllEventsIconBlack } from '@assets/kubernetes/event-icons/all-events-icon-black.icon.svg';
export { default as AnomalyIcon } from '@assets/kubernetes/event-icons/Anomaly.icon.svg';
export { default as ApplicationErrorsIcon } from '@assets/kubernetes/event-icons/application-errors-icon.icon.svg';
export { default as GroupedEventsIcon } from '@assets/kubernetes/event-icons/grouped-events-icon.icon.svg';
export { default as NodeErrorsIcon } from '@assets/kubernetes/event-icons/node-errors-icon.icon.svg';
export { default as NudgebeeIcon } from '@assets/kubernetes/event-icons/nudgebee-icon.icon.svg';
export { default as PodErrorsIcon } from '@assets/kubernetes/event-icons/pod-errors-icon.icon.svg';
export { default as EventSummaryIcon } from '@assets/kubernetes/event-icons/summary-icon.icon.svg';

// app-nodes dropdown icons
export { default as ApplicationsIcon } from '@assets/kubernetes/app-nodes-icons/applications-icon.icon.svg';
export { default as ApplicationsIconblue } from '@assets/kubernetes/app-nodes-icons/ApplicationsIconBlue.icon.svg';
export { default as NamespaceIcon } from '@assets/kubernetes/app-nodes-icons/namespace-icon.icon.svg';
export { default as NodesIcon } from '@assets/kubernetes/app-nodes-icons/nodes-icon.icon.svg';
export { default as AppNodesPodsIcon } from '@assets/kubernetes/app-nodes-icons/pods-icon.icon.svg';
export { default as PVCIcon } from '@assets/kubernetes/app-nodes-icons/PVC-icon.icon.svg';
export { default as PVIcon } from '@assets/kubernetes/app-nodes-icons/PV-icon.icon.svg';
export { default as ServicesIcon } from '@assets/kubernetes/app-nodes-icons/services-icon.icon.svg';
export { default as DataBaseBlueIcon } from '@assets/kubernetes/app-nodes-icons/database-blue.icon.svg';
export { default as QueueBlueIcon } from '@assets/kubernetes/app-nodes-icons/messaging-queue.icon.svg';

//MONITORING
// logs dropdown icons
export { default as LogGroupsIcon } from '@assets/kubernetes/monitoring/log-groups-icon.icon.svg';
export { default as QueryLogIcon } from '@assets/kubernetes/monitoring/query-log-icon.icon.svg';
// traces dropdown icons
export { default as CrossZoneCommunicationIcon } from '@assets/kubernetes/monitoring/cross-zone-communication-icon.icon.svg';
export { default as ServiceMapIcon } from '@assets/kubernetes/monitoring/service-map-icon.icon.svg';
export { default as TracesGroupIcon } from '@assets/kubernetes/monitoring/traces-group-icon.icon.svg';
export { default as LogsTracesIcon } from '@assets/kubernetes/monitoring/traces-icon.icon.svg';
// matrics dropdown icons
export { default as AlertManagerIcon } from '@assets/kubernetes/monitoring/alert-manager-icon.icon.svg';
export { default as AlertSilenceIcon } from '@assets/kubernetes/monitoring/alert-silence-icon.icon.svg';
export { default as MarticsPromQLIcon } from '@assets/kubernetes/monitoring/promQL-icon.icon.svg';
// Others dropdown icons
export { default as SLOInspectionIcon } from '@assets/kubernetes/monitoring/slo-inspection.icon.svg';
export { default as GrafanaIconBlue } from '@assets/kubernetes/monitoring/grafana-icon.icon.svg';
// Security dropdown icon
export { default as CISScanIcon } from '@assets/kubernetes/monitoring/cis_scan.icon.svg';
export { default as SensitiveLogIcon } from '@assets/kubernetes/monitoring/sensitive_logs.icon.svg';
export { default as ImageScanIcon } from '@assets/kubernetes/monitoring/image_scan.icon.svg';
// tools dropdown icons
export { default as CertificateIssuesIcon } from '@assets/kubernetes/monitoring/certificate-issues-icon.icon.svg';
export { default as ClusterUpgradeIcon } from '@assets/kubernetes/monitoring/cluster-upgrade-icon.icon.svg';
export { default as HelmUpgradeIcon } from '@assets/kubernetes/monitoring/helm-upgrade-icon.icon.svg';

// node listing
export * as LocationIcon from '@assets/new/k8s-node-listing/location.svg';
export * as MemoryIcon from '@assets/new/k8s-node-listing/memory.svg';
export * as CpuIcon from '@assets/new/k8s-node-listing/cpu.svg';
export * as SyncIcon from '@assets/new/k8s-node-listing/sync.svg';
// dropdown icons
export * as TableAccordionArrowDownIcon from '@assets/arrow/menu-dropdown-icon.svg';
export { default as TableAccordionArrowIcon } from '@assets/arrow/table-accordion-arrow.icon.svg';
export { default as MenuArrowDownIcon } from '@assets/arrow/table-accordion-arrow.icon.svg';

export * as ScalerNodePoolIcon from '@assets/new-icons/auto-scaler/node-pool.svg';
export * as ScalerNodeClassIcon from '@assets/new-icons/auto-scaler/node-class.svg';
//text uplifters
export * as YellowUnderline from '@assets/Underline/Yellow Underline.png';
export { default as KubernetesClusterIcon } from '@assets/home/new-cluster-icon.icon.svg';
export * as EmojiSmile from '@assets/emoji-smile.svg';
export * as EmojiSad from '@assets/emoji-sad.svg';

// integrations
export * as RabbitmqIcon from '@assets/rabbitmq.svg';
export * as MySqlIcon from '@assets/ou-management/mysql_icon.svg';
export * as RedisLogoIcon from '@assets/redis-logo-icon.svg';
export * as AccountAddedIcon from '@assets/user-management/acc-added-icon.svg';
export * as ActiveIcon from '@assets/user-management/Active-Icon.svg';
export * as InactiveIcon from '@assets/user-management/Inactive-Icon.svg';
export * as RedStarIcon from '@assets/red-star.svg';

// Agents
export * as LlmIcon from '@assets/ask-nudgebee/llm-icon.svg';
export * as McpIcon from '@assets/cloud-account/mcp-icon.png';
export * as DocumentationBlueIcon from '@assets/ask-nudgebee/documentation-blue-icon.svg';
export * as EventBlueIcon from '@assets/ask-nudgebee/event-blue-icon.svg';
export * as EventRCAReportBlueIcon from '@assets/ask-nudgebee/events_rca_report.svg';
export * as GCPBlueIcon from '@assets/ask-nudgebee/gcp-blue-icon.svg';
export * as K8sDebugBlueIcon from '@assets/ask-nudgebee/k8s-debug-blue-icon.svg';
export * as LogQLQueryBlueIcon from '@assets/ask-nudgebee/logql_query-blue-icon.svg';
export * as PromQLQueryBlueIcon from '@assets/ask-nudgebee/prom-ql-blue.svg';
export * as LogsBlueIcon from '@assets/home/logs-icon.icon.svg';
export * as LogAnalysisBlueIcon from '@assets/ask-nudgebee/log-analysis.svg';
export * as OptimizeBlueIcon from '@assets/ask-nudgebee/optimize-blue.svg';
export * as SearchBlueIcon from '@assets/ask-nudgebee/search-blue.svg';
export * as SecurityBlueIcon from '@assets/ask-nudgebee/security-blue.svg';
export * as CustomAgentBlueIcon from '@assets/ask-nudgebee/custom-agent.svg';
export * as DottedLoaderGIF from '@assets/ask-nudgebee/dotted-loader.gif';
export * as TracesBlueIcon from '@assets/ask-nudgebee/traces-blue-icon.svg';
export * as AgentIcon from '@assets/ask-nudgebee/agent-icon.svg';
export * as ToolsIcon from '@assets/ask-nudgebee/tools-icon.svg';
export * as LLMFunctionIcon from '@assets/ask-nudgebee/llm-function-icon.svg';
// Same glyphs as above, recolored to the design system's brand blue
// (#3B82F6) — for menus (AI & Tools) where these sit beside other tabs'
// custom icons that already hardcode that blue, so an unstyled dark/black
// glyph here would be the odd one out.
export * as AgentIconTabBlue from '@assets/ask-nudgebee/agent-icon-blue.svg';
export * as ToolsIconTabBlue from '@assets/ask-nudgebee/tools-icon-blue.svg';
export { default as LLMConsumptionIcon } from '@assets/ask-nudgebee/speedometer-icon.icon.svg';
export * as FollowUpBlueIcon from '@assets/ask-nudgebee/followup-blue.svg';
export * as UploadIcon from '@assets/upload-icon.svg';
export * as PlayCircleIcon from '@assets/play-circle.svg';

export * as LoggleIcon from '@assets/loggly.svg';
export * as ObserveIcon from '@assets/observe-logo.svg';
export * as OpenObserveIcon from '@assets/openobserve-icon.svg';
export * as CubeAPMIcon from '@assets/cubeapm-icon.svg';
export * as AzureAppInsightIcon from '@assets/azure_app_insight.svg';
export * as OpentelemetryIcon from '@assets/opentelemetry.svg';
export * as ChronosphereIcon from '@assets/chronosphere-icon.svg';
export * as VictoriaMetricsIcon from '@assets/victoria-metrics-icon.svg';
export * as Last9Icon from '@assets/last9.svg';
export * as AzureMonitorWebhookIcon from '@assets/azure_webhook.svg';
export * as WorkflowWebhookIcon from '@assets/workflow_webhook.svg';
export * as JaegerIcon from '@assets/jaeger_tracing.svg';
export * as SplunkIcon from '@assets/splunk.svg';
export * as SolarWindsIcon from '@assets/solarwinds.svg';
export * as GitLabIcon from '@assets/gitlab.svg';
export * as GrafanaTempoIcon from '@assets/grafana-tempo.svg';
export * as BitBucketIcon from '@assets/bit_bucket.svg';
export * as OpenSearchIcon from '@assets/opensearch_logo_default.svg';
export * as CloudFoundryIcon from '@assets/cloud-account/cloudfoundry.svg';
export * as SelfHostedIcon from '@assets/cloud-account/self-hosted.svg';

export { default as AzureVMIcon } from '@assets/cloud-account/azure-vm.icon.svg';
export { default as AzureSqlIcon } from '@assets/cloud-account/azure-sql.icon.svg';
export { default as AzureBlobIcon } from '@assets/cloud-account/azure-blob.icon.svg';
export * as AzureDiskIcon from '@assets/cloud-account/azure-disk.svg';
export { default as AWSEC2Icon } from '@assets/cloud-account/ec2-icon.icon.svg';
export { default as AWSRDSIcon } from '@assets/cloud-account/rds-icon.icon.svg';
export { default as AWSS3Icon } from '@assets/cloud-account/s3-icon.icon.svg';
export { default as AWSECSIcon } from '@assets/cloud-account/ecs-icon.icon.svg';
export { default as GCPIcon } from '@assets/cloud-account/gcp-icon.icon.svg';
export { default as AzureIcon } from '@assets/cloud-account/azure-icon.icon.svg';
export * as AWSECRIcon from '@assets/cloud-account/ecr-icon.svg';
export * as AWSNatGatewayIcon from '@assets/cloud-account/aws-nat-gateway.svg';
export * as AWSVPCIcon from '@assets/cloud-account/aws-vpc.svg';
export * as AWSSESIcon from '@assets/cloud-account/aws-ses.svg';
export * as AWSCloudFormationIcon from '@assets/cloud-account/aws-cloudformation.svg';
export * as AWSCloudTrailIcon from '@assets/cloud-account/aws-cloudtrail.svg';
export * as AWSKMSIcon from '@assets/cloud-account/aws-kms.svg';
export * as AWSSQSIcon from '@assets/cloud-account/aws-sqs.svg';
export * as AWSEBSIcon from '@assets/cloud-account/aws-ebs.svg';
export * as AWSSNSIcon from '@assets/cloud-account/aws-sns.svg';
export * as AWSSecurityGroupIcon from '@assets/cloud-account/aws-security-group.svg';
export * as AWSIAMIcon from '@assets/cloud-account/aws-iam.svg';
export * as GCPbigQueryIcon from '@assets/cloud-account/gcp-bigquery.icon.svg';
export { default as GCPComputeEngineIcon } from '@assets/cloud-account/gcp-compute-engine.icon.svg';
export { default as GCPCloudSQLIcon } from '@assets/cloud-account/gcp-cloud-sql.icon.svg';
export { default as GCPCloudStorageIcon } from '@assets/cloud-account/gcp-cloud-storage.icon.svg';
// GCP Service Icons
export * as GCPGKEIcon from '@assets/cloud-account/gcp-gke.icon.svg';
export * as GCPVertexAIIcon from '@assets/cloud-account/gcp-vertex-ai.icon.svg';
export * as GCPCloudRunIcon from '@assets/cloud-account/gcp-cloud-run.icon.svg';
export * as GCPCloudSpannerIcon from '@assets/cloud-account/gcp-cloud-spanner.icon.svg';
export * as GCPAlloyDBIcon from '@assets/cloud-account/gcp-alloydb.icon.svg';
export * as GCPAnthosIcon from '@assets/cloud-account/gcp-anthos.icon.svg';
export * as GCPApigeeIcon from '@assets/cloud-account/gcp-apigee.icon.svg';
export * as GCPDistributedCloudIcon from '@assets/cloud-account/gcp-distributed-cloud.icon.svg';
export * as GCPHyperdiskIcon from '@assets/cloud-account/gcp-hyperdisk.icon.svg';
export * as GCPLookerIcon from '@assets/cloud-account/gcp-looker.icon.svg';
export * as GCPMandiantIcon from '@assets/cloud-account/gcp-mandiant.icon.svg';
export * as GCPSecurityCommandCenterIcon from '@assets/cloud-account/gcp-security-command-center.icon.svg';
export * as GCPSecOpsIcon from '@assets/cloud-account/gcp-secops.icon.svg';
export * as GCPThreatIntelligenceIcon from '@assets/cloud-account/gcp-threat-intelligence.icon.svg';
export * as GCPAIHypercomputerIcon from '@assets/cloud-account/gcp-ai-hypercomputer.icon.svg';
export * as GCPCloudPubSubIcon from '@assets/cloud-account/gcp-cloud-pubsub.icon.svg';
export * as GCPCloudLoadBalancingIcon from '@assets/cloud-account/gcp-cloud-load-balancing.icon.svg';
export * as GCPArtifactRegistryIcon from '@assets/cloud-account/gcp-artifact-registry.icon.svg';
export * as GCPIAMIcon from '@assets/cloud-account/gcp-iam.icon.svg';
// Azure Service Icons (official Microsoft Azure architecture icons).
// VM and SQL reuse the existing AzureVMIcon / AzureSqlIcon exports above.
export * as AzureVMScaleSetsIcon from '@assets/cloud-account/azure-vm-scale-sets.icon.svg';
export * as AzureDisksIcon from '@assets/cloud-account/azure-disks.icon.svg';
export * as AzureDiskSnapshotIcon from '@assets/cloud-account/azure-disk-snapshot.icon.svg';
export * as AzureImageIcon from '@assets/cloud-account/azure-image.icon.svg';
export * as AzureComputeGalleriesIcon from '@assets/cloud-account/azure-compute-galleries.icon.svg';
export * as AzureVirtualNetworksIcon from '@assets/cloud-account/azure-virtual-networks.icon.svg';
export * as AzureSubnetIcon from '@assets/cloud-account/azure-subnet.icon.svg';
export * as AzureNSGIcon from '@assets/cloud-account/azure-nsg.icon.svg';
export * as AzureNetworkInterfacesIcon from '@assets/cloud-account/azure-network-interfaces.icon.svg';
export * as AzurePublicIPIcon from '@assets/cloud-account/azure-public-ip.icon.svg';
export * as AzurePrivateEndpointsIcon from '@assets/cloud-account/azure-private-endpoints.icon.svg';
export * as AzureDNSZonesIcon from '@assets/cloud-account/azure-dns-zones.icon.svg';
export * as AzureLoadBalancersIcon from '@assets/cloud-account/azure-load-balancers.icon.svg';
export * as AzureApplicationGatewaysIcon from '@assets/cloud-account/azure-application-gateways.icon.svg';
export * as AzureStorageAccountsIcon from '@assets/cloud-account/azure-storage-accounts.icon.svg';
export * as AzureFilesharesIcon from '@assets/cloud-account/azure-fileshares.icon.svg';
export * as AzureAppServicesIcon from '@assets/cloud-account/azure-app-services.icon.svg';
export * as AzureAppServicePlansIcon from '@assets/cloud-account/azure-app-service-plans.icon.svg';
export * as AzureStaticAppsIcon from '@assets/cloud-account/azure-static-apps.icon.svg';
export * as AzureKeyVaultsIcon from '@assets/cloud-account/azure-key-vaults.icon.svg';
export * as AzureManagedIdentitiesIcon from '@assets/cloud-account/azure-managed-identities.icon.svg';
export * as AzureServiceBusIcon from '@assets/cloud-account/azure-service-bus.icon.svg';
export * as AzureEventHubsIcon from '@assets/cloud-account/azure-event-hubs.icon.svg';
export * as AzureEventGridTopicsIcon from '@assets/cloud-account/azure-event-grid-topics.icon.svg';
export * as AzureLogicAppsIcon from '@assets/cloud-account/azure-logic-apps.icon.svg';
export * as AzureContainerRegistriesIcon from '@assets/cloud-account/azure-container-registries.icon.svg';
export * as AzureKubernetesServicesIcon from '@assets/cloud-account/azure-kubernetes-services.icon.svg';
export * as AzureDatabricksIcon from '@assets/cloud-account/azure-databricks.icon.svg';
export * as AzureCognitiveServicesIcon from '@assets/cloud-account/azure-cognitive-services.icon.svg';
export * as AzureDatabasePostgreSQLIcon from '@assets/cloud-account/azure-database-postgresql.icon.svg';
export * as AzureDatabaseMySQLIcon from '@assets/cloud-account/azure-database-mysql.icon.svg';
export * as AzureCosmosDBIcon from '@assets/cloud-account/azure-cosmos-db.icon.svg';
export * as AzureCacheRedisIcon from '@assets/cloud-account/azure-cache-redis.icon.svg';
export * as AzureRecoveryServicesVaultsIcon from '@assets/cloud-account/azure-recovery-services-vaults.icon.svg';
export * as AzureDataFactoriesIcon from '@assets/cloud-account/azure-data-factories.icon.svg';
export * as AzureMachineLearningIcon from '@assets/cloud-account/azure-machine-learning.icon.svg';
export * as AzureAutomationAccountsIcon from '@assets/cloud-account/azure-automation-accounts.icon.svg';
export * as AzureSynapseAnalyticsIcon from '@assets/cloud-account/azure-synapse-analytics.icon.svg';
export * as AzureCognitiveSearchIcon from '@assets/cloud-account/azure-cognitive-search.icon.svg';
export * as AzureMonitorIcon from '@assets/cloud-account/azure-monitor.icon.svg';
export * as AzureArcIcon from '@assets/cloud-account/azure-arc.icon.svg';
export * as AzureCommunicationServicesIcon from '@assets/cloud-account/azure-communication-services.icon.svg';
export * as AzureMarketplaceIcon from '@assets/cloud-account/azure-marketplace.icon.svg';
export * as AzureResourceIcon from '@assets/cloud-account/azure-resource.icon.svg';
export * as K8sServiceIcon from '@assets/cloud-account/k8s-service.png';
export * as K8sPVCIcon from '@assets/cloud-account/k8s-pvc.png';
export * as K8sPVIcon from '@assets/cloud-account/k8s-pv.png';
export * as K8sDeploymentIcon from '@assets/cloud-account/k8s-deployment.png';
export * as K8sDaemonSetIcon from '@assets/cloud-account/k8s-daemonset.png';
export * as K8sJobIcon from '@assets/cloud-account/k8s-job.png';
export * as K8sCronJobIcon from '@assets/cloud-account/k8s-cronjob.png';
export * as K8sStatefulSetIcon from '@assets/cloud-account/k8s-statefulset.png';
export * as K8sNodeIcon from '@assets/cloud-account/k8s-node.svg';
export * as K8sPodIcon from '@assets/cloud-account/k8s-pod.svg';
export * as K8sServiceAccountIcon from '@assets/cloud-account/k8s-serviceaccount.svg';
export * as K8sIngressIcon from '@assets/cloud-account/k8s-ingress.svg';
export * as K8sSecretIcon from '@assets/cloud-account/k8s-secret.svg';
export * as K8sConfigMapIcon from '@assets/cloud-account/k8s-configmap.svg';
export * as K8sCRDIcon from '@assets/cloud-account/k8s-crd.svg';

// Workflow Builder
export * as aiAgentIcon from '@assets/workflow_builder/ai-agent.svg';
export * as coreOpsIcon from '@assets/workflow_builder/core-ops.svg';
export * as dataTransformationIcon from '@assets/workflow_builder/data-transformation.svg';
export * as flowLogicIcon from '@assets/workflow_builder/flow-logic.svg';
export * as manualTriggerIcon from '@assets/workflow_builder/manual-icon.svg';
export * as EventIconPurple from '@assets/workflow_builder/event-clock-icon-purple.svg';
export * as workflowAddIcon from '@assets/workflow_builder/workflow-plus-icon.png';
export * as workflowTemplateIcon from '@assets/workflow_builder/workflow-template-icon.png';
export * as workflowAiIcon from '@assets/workflow_builder/workflow-ai-icon.png';

// Workflow Icons
export * as workflowMessagingIcon from '@assets/workflow/messaging.svg';
export * as workflowCalendarIcon from '@assets/workflow/Calendar.svg';
export * as workflowSubWorkflowIcon from '@assets/workflow/Sub-workflow.svg';
export * as SubWorkflowIcon from '@assets/workflow/Sub-workflow.svg';
export * as workflowFormatterIcon from '@assets/workflow/formatter.svg';
export * as workflowUserIcon from '@assets/workflow/User.svg';
export * as workflowFormatIndentIcon from '@assets/workflow/Format-Indent-Increase.svg';
export * as workflowDatabaseIcon from '@assets/workflow/Database.svg';
export * as workflowWebhookIcon from '@assets/workflow/webhook.svg';
export * as alertYellowIcon from '@assets/workflow/alert-yellow.svg';
export * as GrafanaColorIcon from '@assets/grafana.svg';
export * as LogsIcon from '@assets/logs.svg';
export * as MetricsInvestigateIcon from '@assets/metrics.svg';
