import { queryGraphQL } from '@lib/HttpService';

export interface ProductUpdate {
  id: string;
  title: string;
  body: string;
  category?: string | null;
  url?: string | null;
  /** false => shown in the drawer but never highlighted (historical back-catalog). Defaults true. */
  highlight?: boolean;
  /**
   * Optional in-app guided tour to offer for this update — the registry id of a
   * tour in `TOURS` (`@components/common/tour`). When set and the tour both
   * exists and is runnable by the user, the drawer card shows a "Take the tour"
   * launcher; otherwise the card renders without one (on-demand per update).
   * Wire key is snake_case to match the rest of this payload; absent on updates
   * (or backends) that don't declare a tour, which simply hides the launcher.
   */
  tour_id?: string | null;
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
      tour_id
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
