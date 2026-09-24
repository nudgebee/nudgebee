import { useEffect, useRef, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import VisibilityOutlinedIcon from '@mui/icons-material/VisibilityOutlined';
import VisibilityOffOutlinedIcon from '@mui/icons-material/VisibilityOffOutlined';
import apiKnowledgeBase from '@api1/knowledge-base';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Link } from '@ui/Link';
import { CodeBlock } from '@ui/CodeBlock';
import { Banner } from '@ui/Banner';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Label } from '@ui/Label';
import Loader from '@shared/Loader';
import { ds } from '@utils/colors';

const errorMessage = (response, fallback) => response?.errors?.[0]?.message || fallback;

// Fold a freshly loaded page's marks into what is already held. A key the user
// has just changed is kept: the page may have been fetched before that write
// landed, and the server's older value would flip the control back.
const seedCategories = (current, items) => {
  const next = { ...current };
  items.forEach((item) => {
    if (item.document_key && next[item.document_key] === undefined) {
      next[item.document_key] = item.note_category || '';
    }
  });
  return next;
};

// 'none' is the absence of a mark, not a stored value: ToggleGroup in single
// mode needs a chosen option, and the API takes '' to clear.
// It is labelled "Default" because that is what it does — the document gets its
// knowledge base's treatment — and "None" read as "agents ignore it".
//
// No per-option tooltips: MUI's Tooltip labels its child with the tooltip text,
// which would replace "SOP" with a sentence as the button's accessible name.
// The explanation is given once above the list instead.
const CATEGORY_OPTIONS = [
  { value: 'none', label: 'Default' },
  { value: 'fact', label: 'Fact' },
  { value: 'sop', label: 'SOP' },
];

// Lists the documents stored for a knowledge base: title (linked to the source
// page when it has one), a Fact/SOP mark that decides how agents treat the
// document, and an eye toggle that loads and shows the stored content inline.
// Content is fetched on demand and cached per document.
const KBDocumentsModal = ({ open, onClose, accountId, kbId, kbName, canEdit }) => {
  const [documents, setDocuments] = useState([]);
  const [nextOffset, setNextOffset] = useState(null);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState(null);
  const [expandedId, setExpandedId] = useState(null);
  const [contents, setContents] = useState({});
  // Marks are held by document_key rather than id so they survive paging and so
  // the optimistic value and the server's agree on one handle.
  const [categories, setCategories] = useState({});
  const [savingKeys, setSavingKeys] = useState([]);
  const [categoryError, setCategoryError] = useState(null);
  // Bumped whenever the modal target changes so late responses for a previous
  // KB (or a closed modal) are dropped instead of overwriting fresh state.
  const requestKey = useRef(0);

  useEffect(() => {
    const key = ++requestKey.current;
    setDocuments([]);
    setNextOffset(null);
    setError(null);
    setExpandedId(null);
    setContents({});
    setCategories({});
    setSavingKeys([]);
    setCategoryError(null);
    // In-flight requests for the previous target are ignored, so their
    // finally() never clears these; reset them here instead.
    setLoading(false);
    setLoadingMore(false);
    if (!open || !accountId || !kbId) return;
    setLoading(true);
    apiKnowledgeBase
      .getKBDocuments(accountId, kbId)
      .then((response) => {
        if (key !== requestKey.current) return;
        if (response.data) {
          setDocuments(response.data.items);
          setCategories(seedCategories({}, response.data.items));
          setNextOffset(response.data.nextOffset);
        } else {
          setError(errorMessage(response, 'Failed to load documents'));
        }
      })
      .finally(() => {
        if (key === requestKey.current) setLoading(false);
      });
    // Drop late responses once the target changes or the modal unmounts.
    return () => {
      requestKey.current += 1;
    };
  }, [open, accountId, kbId]);

  const loadMore = () => {
    const key = requestKey.current;
    setLoadingMore(true);
    apiKnowledgeBase
      .getKBDocuments(accountId, kbId, nextOffset)
      .then((response) => {
        if (key !== requestKey.current) return;
        if (response.data) {
          setDocuments((prev) => [...prev, ...response.data.items]);
          setCategories((prev) => seedCategories(prev, response.data.items));
          setNextOffset(response.data.nextOffset);
        } else {
          setError(errorMessage(response, 'Failed to load more documents'));
        }
      })
      .finally(() => {
        if (key === requestKey.current) setLoadingMore(false);
      });
  };

  const setCategory = (documentKey, next) => {
    const category = next === 'none' ? '' : next;
    const previous = categories[documentKey] || '';
    if (category === previous) return;
    const key = requestKey.current;
    setCategoryError(null);
    setCategories((prev) => ({ ...prev, [documentKey]: category }));
    setSavingKeys((prev) => [...prev, documentKey]);
    apiKnowledgeBase
      .setKBDocumentCategory(accountId, kbId, documentKey, category)
      .then((response) => {
        if (key !== requestKey.current) return;
        if (!response.data) {
          // Put the control back where it was: the mark decides how agents
          // treat the document, so showing one that was not saved is worse
          // than showing none.
          setCategories((prev) => ({ ...prev, [documentKey]: previous }));
          setCategoryError(errorMessage(response, 'Failed to update document category'));
        }
      })
      .finally(() => {
        if (key !== requestKey.current) return;
        setSavingKeys((prev) => prev.filter((k) => k !== documentKey));
      });
  };

  const toggleContent = (documentId) => {
    if (expandedId === documentId) {
      setExpandedId(null);
      return;
    }
    setExpandedId(documentId);
    if (contents[documentId]?.content != null || contents[documentId]?.loading) return;
    const key = requestKey.current;
    setContents((prev) => ({ ...prev, [documentId]: { loading: true } }));
    apiKnowledgeBase.getKBDocument(accountId, kbId, documentId).then((response) => {
      if (key !== requestKey.current) return;
      setContents((prev) => ({
        ...prev,
        [documentId]: response.data ? { content: response.data.content || '' } : { error: errorMessage(response, 'Failed to load document content') },
      }));
    });
  };

  const renderContent = (documentId) => {
    const state = contents[documentId];
    if (!state || state.loading) return <Loader />;
    if (state.error) return <Banner tone='critical' message={state.error} />;
    if (!state.content) {
      return <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>This document has no stored content.</Typography>;
    }
    return <CodeBlock code={state.content} language='text' wrap maxHeight={360} />;
  };

  return (
    <Modal open={open} handleClose={onClose} title={`Documents: ${kbName || ''}`} width='md'>
      <Box sx={{ padding: ds.space[4] }} data-testid='kb-documents-modal'>
        {loading ? (
          <Loader />
        ) : error && documents.length === 0 ? (
          <Banner tone='critical' message={error} />
        ) : documents.length === 0 ? (
          <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)', textAlign: 'center', py: ds.space[6] }}>
            No documents stored for this knowledge base
          </Typography>
        ) : (
          <Box sx={{ display: 'flex', flexDirection: 'column' }}>
            {/* Only integration documents carry a key; a manual knowledge base is
                marked as a whole, on the knowledge base itself. The legend is shown to
                read-only viewers too, since they see the marks as labels. */}
            {documents.some((doc) => doc.document_key) && (
              // What each option makes an agent do, stated once for the whole list:
              // the options themselves cannot carry it (see CATEGORY_OPTIONS).
              <Box
                component='ul'
                data-testid='kb-document-category-legend'
                sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)', m: 0, pl: ds.space[4], pb: ds.space[2] }}
              >
                <li>
                  <strong>SOP</strong> — a procedure. When this page matches a question, agents read it in full first and follow its steps in order,
                  checking prerequisites before acting.
                </li>
                <li>
                  <strong>Fact</strong> — reference material. Agents use it as supporting information and read only the parts they need.
                </li>
                <li>
                  <strong>Default</strong> — no mark. Pages synced from Confluence or ServiceNow are treated as <strong>Fact</strong>.
                </li>
                {/* Read-only viewers still see marks as labels, so they get the legend
                    too — plus where the marks can be changed. */}
                {!canEdit && <li>Select an account to change how a document is marked.</li>}
              </Box>
            )}
            {documents.map((doc) => {
              const expanded = expandedId === doc.id;
              const documentKey = doc.document_key || '';
              const category = categories[documentKey] || '';
              const saving = savingKeys.includes(documentKey);
              return (
                <Box key={doc.id} sx={{ borderBottom: '1px solid var(--ds-gray-100)', py: ds.space[2] }} data-testid='kb-document-row'>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], minWidth: 0 }}>
                    <Box sx={{ flex: 1, minWidth: 0 }}>
                      {doc.url ? (
                        <Link href={doc.url} openInNew>
                          {doc.title}
                        </Link>
                      ) : (
                        <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-700)', overflowWrap: 'anywhere' }}>
                          {doc.title}
                        </Typography>
                      )}
                    </Box>
                    {canEdit && documentKey ? (
                      // ToggleGroup declares no data-* props, so the handle an
                      // e2e test needs lives on the wrapper.
                      <Box sx={{ display: 'inline-flex' }} data-testid='kb-document-category'>
                        <ToggleGroup
                          selection='single'
                          size='sm'
                          options={saving ? CATEGORY_OPTIONS.map((option) => ({ ...option, disabled: true })) : CATEGORY_OPTIONS}
                          value={category || 'none'}
                          ariaLabel={`Mark ${doc.title} as a fact or an SOP`}
                          onChange={(next) => setCategory(documentKey, next)}
                        />
                      </Box>
                    ) : (
                      category !== '' && <Label text={category === 'sop' ? 'SOP' : 'Fact'} tone='neutral' />
                    )}
                    <Button
                      tone='ghost'
                      size='sm'
                      composition='icon-only'
                      icon={expanded ? <VisibilityOffOutlinedIcon /> : <VisibilityOutlinedIcon />}
                      tooltip={expanded ? 'Hide content' : 'View content'}
                      aria-label={`${expanded ? 'Hide' : 'View'} content of ${doc.title}`}
                      aria-expanded={expanded}
                      data-testid='kb-document-view-content-btn'
                      onClick={() => toggleContent(doc.id)}
                    />
                  </Box>
                  {expanded && <Box sx={{ mt: ds.space[2] }}>{renderContent(doc.id)}</Box>}
                </Box>
              );
            })}
            {categoryError && <Banner tone='critical' message={categoryError} />}
            {error && <Banner tone='critical' message={error} />}
            {nextOffset && (
              <Box sx={{ display: 'flex', justifyContent: 'center', mt: ds.space[3] }}>
                <Button tone='secondary' size='sm' loading={loadingMore} onClick={loadMore} data-testid='kb-documents-load-more-btn'>
                  Load more
                </Button>
              </Box>
            )}
          </Box>
        )}
      </Box>
    </Modal>
  );
};

KBDocumentsModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  accountId: PropTypes.string,
  kbId: PropTypes.string,
  kbName: PropTypes.string,
  // Marks change how agents treat a document, so they follow the tab's write
  // access; a read-only viewer sees the mark but cannot set one.
  canEdit: PropTypes.bool,
};

export default KBDocumentsModal;
