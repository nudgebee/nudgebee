import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

export class IntegrationLocators extends CommonLocators {
  readonly integrationsTab!: Locator;
  readonly ticketingTab!: Locator;
  readonly kubernetestcloudTab!: Locator;
  readonly databaseTab!: Locator;
  readonly observabilityTab!: Locator;
  readonly inmemoryTab!: Locator;
  readonly genericErrorToast: Locator;
  readonly messagingQueueTab!: Locator;
  readonly cicdTab!: Locator;
  readonly docsTab!: Locator;
  readonly serversTab!: Locator;
  readonly reposTab!: Locator;

  readonly azureBtn!: Locator;
  readonly azureHeader!: Locator;
  readonly addAzureAccountBtn!: Locator;
  readonly azDisplayNameInput!: Locator;
  readonly azTenantIdInput!: Locator;
  readonly azClientIdInput!: Locator;
  readonly azClientSecretInput!: Locator;
  readonly azNextToSubscriptionBtn!: Locator;
  readonly azDiscoverSubscriptionBtn!: Locator;
  readonly azNextToReviewBtn!: Locator;
  readonly azNextOnboardSubscriptionBtn!: Locator;
  readonly azSubscriptionIdInput!: Locator;
  readonly azDoneBtn!: Locator;
  readonly azSuccessToast!: Locator;
  readonly azDuplicateErrorToast!: Locator;

  readonly gcpBtn!: Locator;
  readonly addGcpAccountBtn!: Locator;
  readonly gcpDisplayNameInput!: Locator;
  readonly gcpProjectIdInput!: Locator;
  readonly gcpBillingDatasetNameInput!: Locator;
  readonly gcpBillingTableNameInput!: Locator;
  readonly gcpServiceAccountKeyInput!: Locator;
  readonly gcpCheckPermissionsBtn!: Locator;
  readonly gcpNextBtn!: Locator;
  readonly gcpDiscoverProjectsBtn!: Locator;
  readonly gcpNextStep2Btn!: Locator;
  readonly gcpValidateBillingBtn!: Locator;
  readonly gcpSuccessToast!: Locator;
  readonly gcpDuplicateErrorToast!: Locator;
  readonly gcpSaveBtn!: Locator;

  readonly jiraBtn!: Locator;
  readonly addJiraAccountBtn!: Locator;
  readonly jiraNameInput!: Locator;
  readonly jiraAccountUrlInput!: Locator;
  readonly jiraTokenInput!: Locator;
  readonly jiraUsernameInput!: Locator;
  readonly jiraSuccessToast!: Locator;
  readonly jiraDuplicateErrorToast!: Locator;
  readonly jiraTestConnectionSuccessToast!: Locator;
  readonly jiraTestConnectionErrorToast!: Locator;
  readonly jiraSaveButton!: Locator;

  readonly pagerDutyBtn!: Locator;
  readonly addPagerDutyAccountBtn!: Locator;
  readonly pagerDutyNameInput!: Locator;
  readonly pagerDutyEmailInput!: Locator;
  readonly pagerDutyApiTokenInput!: Locator;
  readonly pagerDutySuccessToast!: Locator;
  readonly pagerDutySuccessModalCloseBtn!: Locator;
  readonly pagerDutyDuplicateErrorToast!: Locator;

  readonly postgresqlBtn!: Locator;
  readonly addPostgresqlAccountBtn!: Locator;
  readonly postgresqlAccountIdDropdown: Locator;
  readonly postgresqlAccountIdOption: (name: string) => Locator;
  readonly postgresqlAccountIdCloseBtn: Locator;
  readonly postgresqlHostInput!: Locator;
  readonly postgresqlConfigNameInput!: Locator;
  readonly postgresqlK8sSecretInput!: Locator;
  readonly postgresqlTestConnectionBtn!: Locator;
  readonly postgresqlTestConnectionSuccessToast!: Locator;
  readonly postgresqlDuplicateErrorToast!: Locator;
  readonly postgresqlSuccessToast!: Locator;

  readonly clickhouseBtn!: Locator;
  readonly addClickhouseAccountBtn!: Locator;
  readonly clickhouseAccountIdDropdown: Locator;
  readonly clickhouseAccountIdOption: (name: string) => Locator;
  readonly clickhouseAccountIdCloseBtn: Locator;
  readonly clickhouseHostInput!: Locator;
  readonly clickhouseConfigNameInput!: Locator;
  readonly clickhouseK8sSecretInput!: Locator;
  readonly clickhouseTestConnectionBtn!: Locator;
  readonly clickhouseTestConnectionSuccessToast!: Locator;
  readonly clickhouseSuccessToast!: Locator;

  readonly redisBtn!: Locator;
  readonly addRedisAccountBtn!: Locator;
  readonly redisAccountIdDropdown: Locator;
  readonly redisAccountIdOption: (name: string) => Locator;
  readonly redisAccountIdCloseBtn: Locator;
  readonly redisHostInput!: Locator;
  readonly redisConfigNameInput!: Locator;
  readonly redisK8sSecretInput!: Locator;
  readonly redisTestConnectionBtn!: Locator;
  readonly redisTestConnectionSuccessToast!: Locator;
  readonly redisSuccessToast!: Locator;

  readonly rabbitmqBtn!: Locator;
  readonly addRabbitmqAccountBtn!: Locator;
  readonly rabbitmqAccountIdDropdown: Locator;
  readonly rabbitmqAccountIdOption: (name: string) => Locator;
  readonly rabbitmqAccountIdCloseBtn: Locator;
  readonly rabbitmqHostInput!: Locator;
  readonly rabbitmqConfigNameInput!: Locator;
  readonly rabbitmqK8sSecretInput!: Locator;
  readonly rabbitmqTestConnectionBtn!: Locator;
  readonly rabbitmqTestConnectionSuccessToast!: Locator;
  readonly rabbitmqSuccessToast!: Locator;

  readonly argocdBtn!: Locator;
  readonly addArgocdAccountBtn!: Locator;
  readonly argocdAccountIdDropdown!: Locator;
  readonly argocdAccountIdOption: (name: string) => Locator;
  readonly argocdAccountIdCloseBtn!: Locator;
  readonly argocdServerInput!: Locator;
  readonly argocdConfigNameInput!: Locator;
  readonly argocdK8sSecretInput!: Locator;
  readonly argocdTestConnectionBtn!: Locator;
  readonly argocdTestConnectionSuccessToast!: Locator;
  readonly argocdSuccessToast!: Locator;

  readonly confluenceBtn!: Locator;
  readonly addConfluenceAccountBtn!: Locator;
  readonly confluenceAccountIdDropdown!: Locator;
  readonly confluenceAccountIdOption: (name: string) => Locator;
  readonly confluenceAccountIdCloseBtn!: Locator;
  readonly confluenceHostInput!: Locator;
  readonly confluenceConfigNameInput!: Locator;
  readonly confluenceNamespaceInput!: Locator;
  readonly confluenceTokenInput!: Locator;
  readonly confluenceUserNameInput!: Locator;
  readonly confluenceSuccessToast!: Locator;
  readonly confluenceDuplicateErrorToast!: Locator;

  readonly sshBtn!: Locator;
  readonly addSshAccountBtn!: Locator;
  readonly sshAccountIdDropdown: Locator;
  readonly sshAccountIdOption: (name: string) => Locator;
  readonly sshAccountIdCloseBtn!: Locator;
  readonly sshHostInput!: Locator;
  readonly sshConfigNameInput!: Locator;
  readonly sshK8sSecretInput!: Locator;
  readonly sshTestConnectionBtn: Locator;
  readonly sshTestConnectionSuccessToast: Locator;
  readonly sshDuplicateErrorToast: Locator;
  readonly sshSuccessToast!: Locator;

  readonly githubBtn!: Locator;
  readonly addGithubAccountBtn!: Locator;
  readonly githubMethodUserTokenRadio!: Locator;
  readonly githubNameInput!: Locator;
  readonly githubUsernameInput!: Locator;
  readonly githubTokenInput!: Locator;
  readonly githubSuccessToast!: Locator;
  readonly githubDuplicateErrorToast!: Locator;
  readonly githubTestConnectionSuccessToast!: Locator;
  readonly githubTestConnectionErrorToast!: Locator;
  readonly githubSaveBtn!: Locator;

  readonly zendutyBtn!: Locator;
  readonly addZendutyAccountBtn!: Locator;
  readonly zendutyNameInput!: Locator;
  readonly zendutyEmailInput!: Locator;
  readonly zendutyApiTokenInput!: Locator;
  readonly zendutySuccessToast!: Locator;
  readonly zendutyDuplicateErrorToast!: Locator;
  readonly zendutyTestConnectionSuccessToast!: Locator;
  readonly zendutyTestConnectionErrorToast!: Locator;

  readonly gitlabBtn!: Locator;
  readonly addGitlabAccountBtn!: Locator;
  readonly gitlabNameInput!: Locator;
  readonly gitlabHostUrlInput!: Locator;
  readonly gitlabUsernameInput!: Locator;
  readonly gitlabTokenInput!: Locator;
  readonly gitlabSuccessToast!: Locator;
  readonly gitlabDuplicateErrorToast!: Locator;

  readonly serviceNowBtn!: Locator;
  readonly addServiceNowAccountBtn!: Locator;
  readonly serviceNowNameInput!: Locator;
  readonly serviceNowInstanceUrlInput!: Locator;
  readonly serviceNowUsernameInput!: Locator;
  readonly serviceNowPasswordInput!: Locator;
  readonly serviceNowSuccessToast!: Locator;
  readonly serviceNowDuplicateErrorToast!: Locator;

  // Messaging & Alerting tab
  readonly messagingTab!: Locator;

  // Slack Locators
  readonly slackBtn!: Locator;
  readonly slackIntegrationBox!: Locator;
  readonly addToSlackBtn!: Locator;
  readonly testSlackNotificationBtn!: Locator;
  readonly slackTestSuccessToast!: Locator;
  readonly slackTestErrorToast!: Locator;

  // MS Teams Locators
  readonly msTeamsBtn!: Locator;
  readonly msTeamsIntegrationBox!: Locator;
  readonly addToMsTeamsBtn!: Locator;
  readonly testMsTeamsNotificationBtn!: Locator;
  readonly msTeamsTestSuccessToast!: Locator;
  readonly msTeamsTestErrorToast!: Locator;

  // Google Chat Locators
  readonly googleChatBtn!: Locator;
  readonly googleChatIntegrationBox!: Locator;
  readonly addToGoogleChatBtn!: Locator;
  readonly testGoogleChatNotificationBtn!: Locator;
  readonly googleChatTestSuccessToast!: Locator;
  readonly googleChatTestErrorToast!: Locator;

  // MCP Locators
  readonly mcpBtn!: Locator;
  readonly addMcpAccountBtn!: Locator;
  readonly mcpConfigNameInput!: Locator;
  readonly mcpAccountIdDropdown!: Locator;
  readonly mcpAccountIdOption: (name: string) => Locator;
  readonly mcpUrlInput!: Locator;
  readonly mcpLlmInstructionsInput!: Locator;
  readonly mcpSuccessToast!: Locator;
  readonly mcpDuplicateErrorToast!: Locator;

  constructor(page: Page) {
    super(page);

    this.messagingQueueTab = page.locator("#queue");
    this.integrationsTab = page.locator("#anchor-tab-Integrations");
    this.ticketingTab = page.locator("#ticket");
    this.kubernetestcloudTab = page.locator("#cloud");
    this.databaseTab = page.locator("#database");
    this.observabilityTab = page.getByRole("tab", { name: "Observability" });
    this.inmemoryTab = page.locator("#in-memory");
    this.genericErrorToast = page.locator(
      '[role="alert"].MuiAlert-filledError, [role="alert"].MuiAlert-standardError, .toast-error',
    );
    this.cicdTab = page.locator("#ci_cd");
    this.docsTab = page.locator("#docs");
    this.serversTab = page.locator("#server");
    this.reposTab = page.locator("#repo");

    // Azure Locators
    this.azureBtn = page.locator("#Azure-section-card");
    this.azureHeader = page.getByText("Azure", { exact: true });
    this.addAzureAccountBtn = page.getByRole("button", {
      name: "Add Azure Account",
    });
    this.azDisplayNameInput = page.getByRole("textbox", {
      name: "Display Name",
    });
    this.azTenantIdInput = page.getByRole("textbox", {
      name: "Directory (tenant) ID",
    });
    this.azClientIdInput = page.getByRole("textbox", {
      name: "Application (client) ID",
    });
    this.azClientSecretInput = page.getByRole("textbox", {
      name: "Client Secret",
    });
    this.azNextToSubscriptionBtn = page.locator("#next-to-subscriptions");
    this.azDiscoverSubscriptionBtn = page.locator("#discover-subscriptions");
    this.azNextToReviewBtn = page.locator("#next-to-review");
    this.azNextOnboardSubscriptionBtn = page.locator("#onboard-subscriptions");
    this.azSubscriptionIdInput = page.getByRole("textbox", {
      name: "Subscription ID",
    });
    this.azDoneBtn = page.getByRole("button", { name: "Done" });
    this.azSuccessToast = page.getByText("Onboarded successfully", {
      exact: false,
    });
    this.azDuplicateErrorToast = page.getByText("already exists", {
      exact: false,
    });

    // GCP Locators
    this.gcpBtn = page.locator("#Gcp-section-card");
    this.addGcpAccountBtn = page.locator("#add-gcp-account-btn");
    this.gcpDisplayNameInput = page.getByRole("textbox", {
      name: "Display Name",
    });
    this.gcpProjectIdInput = page.locator("#billing-project-id-label");
    this.gcpBillingDatasetNameInput = page.locator(
      "#billing-dataset-name-label",
    );
    this.gcpBillingTableNameInput = page.locator("#billing-table-name-label");
    this.gcpServiceAccountKeyInput = page.getByRole("textbox", {
      name: "Service Account Key (JSON)",
    });
    this.gcpCheckPermissionsBtn = page.locator("#check-permissions-btn");
    this.gcpNextBtn = page.locator("#next-step1");
    this.gcpDiscoverProjectsBtn = page.locator("#discover-projects-btn");
    this.gcpNextStep2Btn = page.locator("#next-step2");
    this.gcpValidateBillingBtn = page.locator("#validate-billing-btn");
    this.gcpSuccessToast = page.getByText(
      "GCP project(s) onboarded successfully",
      {
        exact: false,
      },
    );
    this.gcpDuplicateErrorToast = page.getByText("already exists", {
      exact: false,
    });
    this.gcpSaveBtn = page.locator("#save-and-continue-btn");

    // Jira Locators
    this.jiraBtn = page.locator("#Jira-section-card");
    this.addJiraAccountBtn = page.locator("#add-jira-account-btn");
    this.jiraNameInput = page.getByRole("textbox", {name: "Name",exact: true,});
    this.jiraAccountUrlInput = page.getByRole("textbox", {name: "Account URL",});
    this.jiraTokenInput = page.getByRole("textbox", { name: "Token" });
    this.jiraUsernameInput = page.getByRole("textbox", { name: "User Name" });
    this.jiraSuccessToast = page.getByText("Jira account added successfully", {exact: false,});
    this.jiraDuplicateErrorToast = page.getByText("already exists", {exact: false,});
    this.jiraTestConnectionSuccessToast = page.getByText("connection successful",{ exact: false },);
    this.jiraTestConnectionErrorToast = page.getByText("connection test failed",{ exact: false },);
    this.jiraSaveButton = page.locator("#create-jira-acc");

    // PagerDuty Locators
    this.pagerDutyBtn = page.locator("#Pagerduty-section-card");
    this.addPagerDutyAccountBtn = page.getByRole("button", {
      name: "Add PagerDuty Account",
    });
    this.pagerDutyNameInput = page.locator("#integration-config-name");
    this.pagerDutyEmailInput = page.locator("#username");
    this.pagerDutyApiTokenInput = page.locator("#password");
    this.pagerDutySuccessToast = page.getByText("Account added successfully", {
      exact: false,
    });
    this.pagerDutySuccessModalCloseBtn = page.getByRole("button", {
      name: "Close",
    });
    this.pagerDutyDuplicateErrorToast = page.getByText("already exists", {
      exact: false,
    });

    // PostgreSQL Locators
    this.postgresqlBtn = page.locator("#Postgres-section-card");
    this.addPostgresqlAccountBtn = page.getByRole("button", {
      name: "Add Postgres Account",
    });
    this.postgresqlAccountIdDropdown = page.locator(
      "#auto-complete-account-id",
    );

    this.postgresqlAccountIdOption = (name: string) =>
      page.locator("div").filter({ hasText: new RegExp(`^${name}$`) });

    this.postgresqlAccountIdCloseBtn = page.getByRole("button", {
      name: "Close",
    });
    this.postgresqlHostInput = page.getByRole("textbox", { name: "Host" });
    this.postgresqlConfigNameInput = page.getByRole("textbox", {
      name: "Integration Config Name",
    });
    this.postgresqlK8sSecretInput = page.getByRole("textbox", {
      name: "K8s Secret",
    });
    this.postgresqlTestConnectionBtn = page.locator("#test-connection-btn");
    this.postgresqlTestConnectionSuccessToast = page.getByText(
      "Postgresql connection successful",
      { exact: false },
    );
    this.postgresqlDuplicateErrorToast = page.getByText("already exists", {
      exact: false,
    });
    this.postgresqlSuccessToast = page.getByText(
      "Postgres account created successfully",
      {
        exact: false,
      },
    );

    // Clickhouse Locators
    this.clickhouseBtn = page.locator("#Clickhouse-section-card");
    this.addClickhouseAccountBtn = page.getByRole("button", {
      name: "Add Clickhouse Account",
    });
    this.clickhouseAccountIdDropdown = page.locator(
      "#auto-complete-account-id",
    );

    this.clickhouseAccountIdOption = (name: string) =>
      page.locator("div").filter({ hasText: new RegExp(`^${name}$`) });

    this.clickhouseAccountIdCloseBtn = page.getByRole("button", {
      name: "Close",
    });
    this.clickhouseHostInput = page.getByRole("textbox", { name: "Host" });
    this.clickhouseConfigNameInput = page.getByRole("textbox", {
      name: "Integration Config Name",
    });
    this.clickhouseK8sSecretInput = page.getByRole("textbox", {
      name: "K8s Secret",
    });
    this.clickhouseTestConnectionBtn = page.locator("#test-connection-btn");
    this.clickhouseTestConnectionSuccessToast = page.getByText(
      "Clickhouse connection successful",
      { exact: false },
    );
    this.clickhouseSuccessToast = page.getByText(
      "Clickhouse account created successfully",
      {
        exact: false,
      },
    );

    // Redis Locators
    this.redisBtn = page.locator("#Redis-section-card");
    this.addRedisAccountBtn = page.getByRole("button", {
      name: "Add Redis Account",
    });
    this.redisAccountIdDropdown = page.locator("#auto-complete-account-id");
    this.redisAccountIdOption = (name: string) =>
      page.locator("div").filter({ hasText: new RegExp(`^${name}$`) });
    this.redisAccountIdCloseBtn = page.getByRole("button", {
      name: "Close",
    });
    this.redisHostInput = page.getByRole("textbox", { name: "Host" });
    this.redisConfigNameInput = page.getByRole("textbox", {
      name: "Integration Config Name",
    });
    this.redisK8sSecretInput = page.getByRole("textbox", {
      name: "K8s Secret",
    });
    this.redisTestConnectionBtn = page.locator("#test-connection-btn");
    this.redisTestConnectionSuccessToast = page.getByText(
      "Redis connection successful",
      { exact: false },
    );
    this.redisSuccessToast = page.getByText(
      "Redis account created successfully",
      {
        exact: false,
      },
    );

    // RabbitMQ Locators
    this.rabbitmqBtn = page.locator("#Rabbitmq-section-card");
    this.addRabbitmqAccountBtn = page.getByRole("button", {
      name: "Add Rabbitmq Account",
    });
    this.rabbitmqAccountIdDropdown = page.locator("#auto-complete-account-id");
    this.rabbitmqAccountIdOption = (name: string) =>
      page.locator("div").filter({ hasText: new RegExp(`^${name}$`) });
    this.rabbitmqAccountIdCloseBtn = page.getByRole("button", {
      name: "Close",
    });
    this.rabbitmqHostInput = page.getByRole("textbox", { name: "Host" });
    this.rabbitmqConfigNameInput = page.getByRole("textbox", {
      name: "Integration Config Name",
    });
    this.rabbitmqK8sSecretInput = page.getByRole("textbox", {
      name: "K8s Secret",
    });
    this.rabbitmqTestConnectionBtn = page.locator("#test-connection-btn");
    this.rabbitmqTestConnectionSuccessToast = page.getByText(
      "Rabbitmq connection successful",
      { exact: false },
    );
    this.rabbitmqSuccessToast = page.getByText(
      "RabbitMQ account created successfully",
      {
        exact: false,
      },
    );

    // Argocd Locators
    this.argocdBtn = page.locator("#Argocd-section-card");
    this.addArgocdAccountBtn = page.getByRole("button", {
      name: "Add Argocd Account",
    });
    this.argocdAccountIdDropdown = page.locator("#auto-complete-account-id");
    this.argocdAccountIdOption = (name: string) =>
      page.locator("div").filter({ hasText: new RegExp(`^${name}$`) });
    this.argocdAccountIdCloseBtn = page.getByRole("button", {
      name: "Close",
    });
    this.argocdConfigNameInput = page.getByRole("textbox", {
      name: "Integration Config Name",
    });
    this.argocdK8sSecretInput = page.getByRole("textbox", {
      name: "K8s Secret",
    });
    this.argocdServerInput = page.getByRole("textbox", {
      name: "Server",
      exact: true,
    });
    this.argocdTestConnectionBtn = page.locator("#test-connection-btn");
    this.argocdTestConnectionSuccessToast = page.getByText(
      "Argocd connection successful",
      { exact: false },
    );
    this.argocdSuccessToast = page.getByText(
      "Argocd account created successfully",
      {
        exact: false,
      },
    );

    // Confluence Locators
    this.confluenceBtn = page.locator("#Confluence-section-card");
    this.addConfluenceAccountBtn = page.locator("#add-confluence-account-btn");
    this.confluenceAccountIdDropdown = page.locator(
      "#auto-complete-account-id",
    );
    this.confluenceAccountIdOption = (name: string) =>
      page.locator("div").filter({ hasText: new RegExp(`^${name}$`) });
    this.confluenceAccountIdCloseBtn = page.locator(
      "#auto-complete-account-id",
    );
    this.confluenceHostInput = page.locator("#host");
    this.confluenceConfigNameInput = page.locator("#integration-config-name");
    this.confluenceNamespaceInput = page.locator("#namespace");
    this.confluenceTokenInput = page.locator("#token");
    this.confluenceUserNameInput = page.locator("#username");
    this.confluenceSuccessToast = page.getByText(
      "Confluence account created successfully",
      {
        exact: false,
      },
    );
    this.confluenceDuplicateErrorToast = page.getByText("already exists", {
      exact: false,
    });

    // SSH Locators
    this.sshBtn = page.locator("#Ssh-section-card");
    this.addSshAccountBtn = page.locator("#add-ssh-account-btn");
    this.sshAccountIdDropdown = page.locator("#auto-complete-account-id");
    this.sshAccountIdOption = (name: string) =>
      page.locator("div").filter({ hasText: new RegExp(`^${name}$`) });
    this.sshAccountIdCloseBtn = page.getByRole("button", {
      name: "Close",
    });
    this.sshHostInput = page.locator("#host");
    this.sshConfigNameInput = page.locator("#integration-config-name");
    this.sshK8sSecretInput = page.locator("#k8s-secret");
    this.sshTestConnectionBtn = page.locator("#test-connection-btn");
    this.sshTestConnectionSuccessToast = page.getByText(
      "Ssh connection successful",
      { exact: false },
    );
    this.sshDuplicateErrorToast = page.getByText("already exists", {
      exact: false,
    });
    this.sshSuccessToast = page.getByText("Ssh account created successfully", {
      exact: false,
    });

    // Github Locators
    this.githubBtn = page.locator("#Github-section-card");
    this.addGithubAccountBtn = page.locator("#add-github-account-btn");
    this.githubMethodUserTokenRadio = page.locator("#user-token-label");
    this.githubNameInput = page.locator("#githubName");
    this.githubUsernameInput = page.locator("#githubUserName");
    this.githubTokenInput = page.locator("#githubToken");
    this.githubSuccessToast = page.getByText("GitHub account added successfully",{exact: false,},);
    this.githubDuplicateErrorToast = page.getByText("already exists", {exact: false,});
    this.githubTestConnectionSuccessToast = page.getByText("connection successful",{ exact: false },);
    this.githubTestConnectionErrorToast = page.getByText("connection test failed",{ exact: false },);
    this.githubSaveBtn = page.locator("#create-github-acc");

    // Zenduty Locators
    this.zendutyBtn = page.locator("#Zenduty-section-card");
    this.addZendutyAccountBtn = page.locator("#add-zenduty-account-btn");
    this.zendutyNameInput = page.locator("#zenDutyName");
    this.zendutyEmailInput = page.locator("#zenDutyAccountName");
    this.zendutyApiTokenInput = page.locator("#zenDutyToken");
    this.zendutySuccessToast = page.getByText("Account added successfully", {
      exact: false,
    });
    this.zendutyDuplicateErrorToast = page.getByText("already exists", {
      exact: false,
    });
    this.zendutyTestConnectionSuccessToast = page.getByText(
      "connection successful",
      { exact: false },
    );
    this.zendutyTestConnectionErrorToast = page.getByText(
      "connection test failed",
      { exact: false },
    );

    // GitLab Locators
    this.gitlabBtn = page.locator("#Gitlab-section-card");
    this.addGitlabAccountBtn = page.locator("#add-gitlab-account-btn");
    this.gitlabNameInput = page.locator("#gitlabName");
    this.gitlabHostUrlInput = page.locator("#gitlabUrl");
    this.gitlabUsernameInput = page.locator("#gitlabUsername");
    this.gitlabTokenInput = page.locator("#gitlabToken");
    this.gitlabSuccessToast = page.getByText(
      "GitLab account created successfully",
      {
        exact: false,
      },
    );
    this.gitlabDuplicateErrorToast = page.getByText("already exists", {
      exact: false,
    });

    // ServiceNow Locators
    this.serviceNowBtn = page.locator("#Servicenow-section-card");
    this.addServiceNowAccountBtn = page.locator("#add-servicenow-account-btn");
    this.serviceNowNameInput = page.locator("#accountName");
    this.serviceNowInstanceUrlInput = page.locator("#accountUrl");
    this.serviceNowUsernameInput = page.locator("#accountUsername");
    this.serviceNowPasswordInput = page.locator("#accountPassword");
    this.serviceNowSuccessToast = page.getByText("Account added successfully", {
      exact: false,
    });
    this.serviceNowDuplicateErrorToast = page.getByText("already exists", {
      exact: false,
    });

    // Messaging & Alerting tab
    this.messagingTab = page.locator("#messaging");

    // Slack Locators
    // Section card ID computed from provider "SLACK" → "Slack" → "Slack-section-card"
    this.slackBtn = page.locator("#Slack-section-card");
    this.slackIntegrationBox = page.locator("#slack-integrations");
    this.addToSlackBtn = page.locator("#add-to-slack-btn");
    this.testSlackNotificationBtn = page.locator("#test-slack-btn");
    this.slackTestSuccessToast = page.getByText(
      "Test notification sent to Slack successfully!",
      { exact: false },
    );
    this.slackTestErrorToast = page.getByText(
      "Failed to send test notification to Slack",
      { exact: false },
    );

    // MS Teams Locators
    // Section card ID computed from provider "MSTEAMS" → "Msteams" → "Msteams-section-card"
    this.msTeamsBtn = page.locator("#Msteams-section-card");
    this.msTeamsIntegrationBox = page.locator("#ms_teams-integrations");
    this.addToMsTeamsBtn = page.locator("#add-to-ms-teams-btn");
    this.testMsTeamsNotificationBtn = page.locator("#test-ms-teams-btn");
    this.msTeamsTestSuccessToast = page.getByText(
      "Test notification sent to Ms Teams successfully!",
      { exact: false },
    );
    this.msTeamsTestErrorToast = page.getByText(
      "Failed to send test notification to Ms Teams",
      { exact: false },
    );

    // Google Chat Locators
    // Section card ID computed from provider "GOOGLE_CHAT" → "Google-Chat" → "Google-Chat-section-card"
    this.googleChatBtn = page.locator("#Google-Chat-section-card");
    this.googleChatIntegrationBox = page.locator("#google_chat-integrations");
    this.addToGoogleChatBtn = page.locator("#add-to-google-chat-btn");
    this.testGoogleChatNotificationBtn = page.locator("#test-google-chat-btn");
    this.googleChatTestSuccessToast = page.getByText(
      "Test notification sent to Google Chat successfully!",
      { exact: false },
    );
    this.googleChatTestErrorToast = page.getByText(
      "Failed to send test notification to Google Chat",
      { exact: false },
    );

    // MCP Locators
    this.mcpBtn = page.locator("#Mcp-section-card");
    this.addMcpAccountBtn = page.locator("#add-mcp-account-btn");
    this.mcpConfigNameInput = page.locator("#integration-config-name");
    this.mcpAccountIdDropdown = page.locator("#auto-complete-account-id");
    this.mcpAccountIdOption = (name: string) =>
      page.locator("div").filter({ hasText: new RegExp(`^${name}$`) });
    this.mcpUrlInput = page.locator("#url");
    this.mcpLlmInstructionsInput = page.locator("#llm-instructions");
    this.mcpSuccessToast = page.getByText("account created successfully", {
      exact: false,
    });
    this.mcpDuplicateErrorToast = page.getByText("already exists", {
      exact: false,
    });
  }

  getIntegrationByName(name: string): Locator {
    return this.getExactText(name);
  }

  getSubscriptionID(key: string): Locator {
    return this.getExactText(key);
  }
}
