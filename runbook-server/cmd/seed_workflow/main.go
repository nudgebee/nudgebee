package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
)

func main() {
	dbURL := os.Getenv("AUTO_PILOT_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgresql://postgres:postgrespassword@127.0.0.1:5432/nudgebee?sslmode=disable"
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	// 1. Get or create user
	var userID string
	err = db.QueryRow("SELECT id FROM public.users LIMIT 1").Scan(&userID)
	if err == sql.ErrNoRows {
		userID = uuid.NewString()
		_, err = db.Exec(`
			INSERT INTO public.users (id, username, display_name, status)
			VALUES ($1, 'dev@example.com', 'Dev User', 'active')
		`, userID)
		if err != nil {
			log.Fatalf("Failed to insert user: %v", err)
		}
		fmt.Printf("Created seed user: %s\n", userID)
	} else if err != nil {
		log.Fatalf("Failed to query user: %v", err)
	}

	// 2. Get or create tenant
	var tenantID string
	err = db.QueryRow("SELECT id FROM public.tenant LIMIT 1").Scan(&tenantID)
	if err == sql.ErrNoRows {
		tenantID = uuid.NewString()
		_, err = db.Exec(`
			INSERT INTO public.tenant (id, name, created_by, updated_by)
			VALUES ($1, 'Default Tenant', $2, $2)
		`, tenantID, userID)
		if err != nil {
			log.Fatalf("Failed to insert tenant: %v", err)
		}
		fmt.Printf("Created seed tenant: %s\n", tenantID)
	} else if err != nil {
		log.Fatalf("Failed to query tenant: %v", err)
	}

	// 2b. Link user to tenant in tenant_users
	var tenantUserID string
	err = db.QueryRow("SELECT id FROM public.tenant_users WHERE tenant = $1 AND \"user\" = $2", tenantID, userID).Scan(&tenantUserID)
	if err == sql.ErrNoRows {
		tenantUserID = uuid.NewString()
		_, err = db.Exec(`
			INSERT INTO public.tenant_users (id, tenant, "user", created_by, updated_by)
			VALUES ($1, $2, $3, $3, $3)
		`, tenantUserID, tenantID, userID)
		if err != nil {
			log.Fatalf("Failed to insert tenant_users: %v", err)
		}
		fmt.Printf("Linked user to tenant in tenant_users: %s\n", tenantUserID)
	} else if err != nil {
		log.Fatalf("Failed to query tenant_users: %v", err)
	}

	// 2c. Assign tenant_admin role to user in user_roles
	var tenantRoleID string
	err = db.QueryRow("SELECT id FROM public.user_roles WHERE user_id = $1 AND role = 'tenant_admin'", userID).Scan(&tenantRoleID)
	if err == sql.ErrNoRows {
		tenantRoleID = uuid.NewString()
		_, err = db.Exec(`
			INSERT INTO public.user_roles (id, role, user_id, entity_type, entity_id, created_by)
			VALUES ($1, 'tenant_admin', $2, 'tenant', $3, $2)
		`, tenantRoleID, userID, tenantID)
		if err != nil {
			log.Fatalf("Failed to insert tenant_admin role: %v", err)
		}
		fmt.Printf("Assigned tenant_admin role: %s\n", tenantRoleID)
	} else if err != nil {
		log.Fatalf("Failed to query user_roles: %v", err)
	}

	// Link user to tenant auth
	var userAuthID string
	err = db.QueryRow("SELECT id FROM public.user_auths WHERE \"user\" = $1", userID).Scan(&userAuthID)
	if err == sql.ErrNoRows {
		userAuthID = uuid.NewString()
		expiresAt := time.Now().AddDate(1, 0, 0)
		_, err = db.Exec(`
			INSERT INTO public.user_auths 
			(id, credential, provider_type, "user", expires_at, status, account_id, provider)
			VALUES ($1, 'dev@example.com', 'email', $2, $3, 'active', 'dev@example.com', 'email')
		`, userAuthID, userID, expiresAt)
		if err != nil {
			fmt.Printf("Warning: failed to insert user auth: %v (continuing)\n", err)
		} else {
			fmt.Printf("Created user auth: %s\n", userAuthID)
		}
	}

	// 3. Get or create first cloud account ID
	var accountID string
	err = db.QueryRow("SELECT id FROM public.cloud_accounts LIMIT 1").Scan(&accountID)
	if err == sql.ErrNoRows {
		accountID = uuid.NewString()
		_, err = db.Exec(`
			INSERT INTO public.cloud_accounts 
			(id, cloud_provider, account_number, account_name, created_by, updated_by, billing_source, tenant, account_type)
			VALUES ($1, 'AWS', '123456789012', 'Local Test Account', $2, $2, 'aws_cur', $3, 'aws')
		`, accountID, userID, tenantID)
		if err != nil {
			log.Fatalf("Failed to insert dummy cloud account: %v", err)
		}
		fmt.Printf("Created dummy cloud account: %s\n", accountID)
	} else if err != nil {
		log.Fatalf("Failed to query cloud accounts: %v", err)
	}

	// 3b. Assign account_admin role to user in user_roles for this cloud account
	var accountRoleID string
	err = db.QueryRow("SELECT id FROM public.user_roles WHERE user_id = $1 AND role = 'account_admin' AND entity_id = $2", userID, accountID).Scan(&accountRoleID)
	if err == sql.ErrNoRows {
		accountRoleID = uuid.NewString()
		_, err = db.Exec(`
			INSERT INTO public.user_roles (id, role, user_id, entity_type, entity_id, created_by)
			VALUES ($1, 'account_admin', $2, 'account', $3, $2)
		`, accountRoleID, userID, accountID)
		if err != nil {
			log.Fatalf("Failed to insert account_admin role: %v", err)
		}
		fmt.Printf("Assigned account_admin role: %s\n", accountRoleID)
	} else if err != nil {
		log.Fatalf("Failed to query user_roles: %v", err)
	}

	// 4. Create workflow
	workflowID := uuid.NewString()
	_, err = db.Exec(`
		INSERT INTO public.workflows 
		(id, tenant_id, account_id, name, definition, tags, status, created_by, updated_by)
		VALUES ($1, $2, $3, 'Demo Seeded Workflow', '{}'::jsonb, '[]'::jsonb, 'ACTIVE', $4, $4)
	`, workflowID, tenantID, accountID, userID)
	if err != nil {
		log.Fatalf("Failed to insert workflow: %v", err)
	}
	fmt.Printf("Inserted workflow: %s\n", workflowID)

	// 5. Create version 1 (v1)
	v1ID := uuid.NewString()
	_, err = db.Exec(`
		INSERT INTO public.workflow_versions 
		(id, workflow_id, version_number, definition, source, name, description, created_by, status)
		VALUES ($1, $2, 1, '{"nodes": []}'::jsonb, 'publish', 'V1 Draft', 'Initial version of dummy workflow', $3, 'ACTIVE')
	`, v1ID, workflowID, userID)
	if err != nil {
		log.Fatalf("Failed to insert workflow version v1: %v", err)
	}
	fmt.Printf("Inserted workflow version v1: %s\n", v1ID)

	// 6. Create version 2 (v2)
	v2ID := uuid.NewString()
	_, err = db.Exec(`
		INSERT INTO public.workflow_versions 
		(id, workflow_id, version_number, definition, source, name, description, created_by, status)
		VALUES ($1, $2, 2, '{"nodes": []}'::jsonb, 'publish', 'V2 Release', 'Second version with workflow tweaks', $3, 'ACTIVE')
	`, v2ID, workflowID, userID)
	if err != nil {
		log.Fatalf("Failed to insert workflow version v2: %v", err)
	}
	fmt.Printf("Inserted workflow version v2: %s\n", v2ID)

	// 7. Update workflow to point to live version v2 and draft version v2
	_, err = db.Exec(`
		UPDATE public.workflows 
		SET live_version_id = $1, draft_version_id = $1 
		WHERE id = $2
	`, v2ID, workflowID)
	if err != nil {
		log.Fatalf("Failed to update workflow live/draft pointers: %v", err)
	}

	fmt.Println("\nSuccessfully seeded dummy workflow with V1 and V2 versions!")
	fmt.Printf("Workflow ID: %s\n", workflowID)
}
