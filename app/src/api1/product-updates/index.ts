import { queryGraphQL } from '@lib/HttpService';

export interface ProductUpdate {
  id: string;
  title: string;
  body: string;
  category?: string | null;
  url?: string | null;
  /** false => shown in the drawer but never highlighted (historical back-catalog). Defaults true. */
  highlight?: boolean;
  published_at: string;
}

const PRODUCT_UPDATES_LIST = `
query ProductUpdatesList {
  product_updates_list {
    data {
      id
      title
      body
      category
      url
      highlight
      published_at
    }
  }
}
`;

/**
 * Fetch the product updates (changelog), newest first. Sourced from a JSON file
 * in nudgebee-docs (no database) — see the productupdates backend service.
 */
export async function listProductUpdates(): Promise<ProductUpdate[]> {
  const response = await queryGraphQL(PRODUCT_UPDATES_LIST, 'ProductUpdatesList', {});
  // Fail closed: surface GraphQL/gateway errors instead of silently returning [].
  const errors = response?.data?.errors;
  if (Array.isArray(errors) && errors.length > 0) {
    throw new Error(errors[0]?.message || 'Failed to load product updates');
  }
  return response?.data?.data?.product_updates_list?.data ?? [];
}

const apiProductUpdates = {
  listProductUpdates,
};

export default apiProductUpdates;
