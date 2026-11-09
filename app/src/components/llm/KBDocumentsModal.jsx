import { useEffect, useRef, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import VisibilityOutlinedIcon from '@mui/icons-material/VisibilityOutlined';
import VisibilityOffOutlinedIcon from '@mui/icons-material/VisibilityOffOutlined';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import KeyboardArrowUpIcon from '@mui/icons-material/KeyboardArrowUp';
import apiKnowledgeBase from '@api1/knowledge-base';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import { Link } from '@ui/Link';
import { CodeBlock } from '@ui/CodeBlock';
import { Banner } from '@ui/Banner';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Label } from '@ui/Label';
import { Chip } from '@ui/Chip';
import Tooltip from '@ui/Tooltip';
import Loader from '@shared/Loader';
import MarkDowns from '@shared/viewers/MarkDowns';
import DOMPurify from 'dompurify';
import { ds } from '@utils/colors';
import DocumentDetailBody from '@components/llm/KBDocumentDetailBody';
import { summariseClassifier } from '@components/llm/kbDocumentClassification';

// A stored PDF arrives base64-encoded. Browsers refuse a `data:` URI in a frame, so
// it is turned into a blob the frame can load, and revoked when the panel closes.
const PdfDocument = ({ base64, title }) => {
  const [url, setUrl] = useState(null);
  useEffect(() => {
    let objectUrl;
    try {
      const binary = atob(base64);
      // A loop rather than Uint8Array.from(binary, fn): an upload runs to 5 MiB, so
      // the mapped form is millions of callback invocations on the main thread for a
      // conversion that happens every time the document is opened.
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
      objectUrl = URL.createObjectURL(new Blob([bytes], { type: 'application/pdf' }));
      setUrl(objectUrl);
    } catch (err) {
      // The banner below tells the reader the file could not be read; this says why,
      // since a decode failure here means what was stored is not the file we think.
      console.error('kb: stored PDF could not be decoded', err);
      setUrl(null);
    }
    return () => {
      if (objectUrl) URL.revokeObjectURL(objectUrl);
    };
  }, [base64]);

  if (!url) {
    return <Banner tone='critical' message='This file could not be read. Re-upload it to view it here.' />;
  }
  // Not sandboxed: the only sandbox that still loads a blob: URL has to keep
  // allow-same-origin, which a frame can then use to drop its own sandbox — so it
  // bought nothing, and Brave blocked the frame outright. The download link below
  // is the way out for anyone who would rather not view it inline.
  return (
    <Box data-testid='kb-document-pdf'>
      <iframe src={url} title={title} style={{ width: '100%', height: 360, border: 'none' }} />
      <Link href={url} download={title}>
        Download {title}
      </Link>
    </Box>
  );
};

PdfDocument.propTypes = { base64: PropTypes.string.isRequired, title: PropTypes.string };

const errorMessage = (response, fallback) => response?.errors?.[0]?.message || fallback;

// Records each document's mark from a freshly loaded page. A mark the user
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

// What the classifier decided about one page, kept apart from the user's Fact/SOP mark: the
// mark is a decision a person made, this is a suggestion, and the legend says which wins.
// A page with no stored label says so instead of showing nothing. The chips use the smallest readable tier (14px):
// they are a secondary signal on a row whose primary content is the title and the mark. Each element has its own
// role so the eye can follow them: the title is the only blue, the kind is the one colour (SOP green, knowledge
// neutral), the shape is a quiet outline, and capabilities have their own hue and appear only when there are some.
const ClassifierLine = ({ doc }) => {
  const summary = summariseClassifier(doc);
  const count = summary.classified ? summary.capabilities.length : 0;
  return (
    <Box data-testid='kb-document-classification' sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: ds.space[1], mt: ds.space[1] }}>
      {summary.classified ? (
        <>
          <Chip variant='tag' size='2xs' tone={summary.kind === 'sop' ? 'success' : 'neutral'}>
            {`Classifier: ${summary.kindLabel}`}
          </Chip>
          {summary.shapeLabel && (
            <Chip variant='tag' size='2xs' tone='subtle'>
              {summary.shapeLabel}
            </Chip>
          )}
          {count > 0 && (
            <Tooltip title={summary.capabilities.join(', ')} placement='top'>
              <Box component='span' sx={{ display: 'inline-flex', cursor: 'help' }}>
                <Chip variant='tag' size='2xs' hue='violet'>
                  {count} capabilit{count === 1 ? 'y' : 'ies'}
                </Chip>
              </Box>
            </Tooltip>
          )}
        </>
      ) : (
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>Not classified</Typography>
      )}
    </Box>
  );
};

ClassifierLine.propTypes = {
  doc: PropTypes.shape({
    kind: PropTypes.string,
    shape: PropTypes.string,
    capabilities: PropTypes.arrayOf(PropTypes.string),
  }).isRequired,
};

// 'none' is the absence of a mark, not a stored value: ToggleGroup in single
// mode needs a chosen option, and the API takes '' to clear. It is labelled
// "Default" because that is what it does — the document gets its knowledge
// base's treatment — and "None" read as "agents ignore it".
//
// No per-option tooltips: ToggleGroup renders a tooltip as the button's
// aria-label, which would replace "SOP" with a sentence as the button's
// accessible name. The explanation is given once above the list instead.
const CATEGORY_OPTIONS = [
  { value: 'none', label: 'Default' },
  { value: 'fact', label: 'Fact' },
  { value: 'sop', label: 'SOP' },
];

// Lists the documents stored for a knowledge base: title (linked to the source
// page when it has one), a Fact/SOP mark that decides how agents treat the
// document, and an eye toggle that loads and shows the stored content inline.
// Content is fetched on demand and cached per document.
// A wiki page's own links point back at the site it came from. Left alone they
// navigate the reader out of the app and lose the modal, so they are sent to a new
// tab — with rel, because an opened tab can otherwise reach back through opener.
// The hook is added and removed around the one call rather than registered globally:
// DOMPurify is shared with every other view that sanitizes, and a global hook would
// silently rewrite their links too.
const openLinksInANewTab = (node) => {
  if (node.tagName === 'A' && node.hasAttribute('href')) {
    node.setAttribute('target', '_blank');
    node.setAttribute('rel', 'noopener noreferrer');
  }
};

const sanitizeStoredHtml = (html) => {
  DOMPurify.addHook('afterSanitizeAttributes', openLinksInANewTab);
  try {
    return DOMPurify.sanitize(html, { ADD_ATTR: ['target', 'rel'] });
  } finally {
    // Removed by identity, so this can only ever take back the hook added above.
    DOMPurify.removeHook('afterSanitizeAttributes', openLinksInANewTab);
  }
};

// A scraped page keeps the site's own image URLs, and a wiki serves those only to a
// browser already holding that site's session. The reader got a broken glyph and no
// reason for it. This says what happened, where the picture was, and keeps the alt
// text — often the only description of the picture the page carries.
const unavailableImageNote = (doc, img, hasSourceLink) => {
  const note = doc.createElement('span');
  note.setAttribute('data-testid', 'kb-document-image-unavailable');
  Object.assign(note.style, {
    display: 'inline-block',
    padding: `${ds.space[1]} ${ds.space[2]}`,
    border: `1px dashed ${ds.gray[300]}`,
    borderRadius: ds.radius.sm,
    color: ds.gray[500],
    fontSize: 'var(--ds-text-caption)',
  });
  const alt = (img.getAttribute('alt') || '').trim();
  // Only offer the title when it is actually a link: a document with no url renders
  // its title as plain text, and telling the reader to click it would send them
  // hunting for something that is not there.
  const wayOut = hasSourceLink ? ' Open the page from its title above to see it.' : '';
  note.textContent = `${alt ? `Image unavailable — ${alt}.` : 'Image unavailable.'}${wayOut}`;
  note.title = 'This page stores the image on the source site, which needs a sign-in this view does not have.';
  return note;
};

const StoredHtml = ({ html, hasSourceLink }) => {
  const containerRef = useRef(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return undefined;
    }
    const replace = (img) => {
      if (img.parentNode) {
        img.parentNode.replaceChild(unavailableImageNote(container.ownerDocument, img, hasSourceLink), img);
      }
    };
    // `error` does not bubble, so it is caught on the way down instead of on the way up.
    const onError = (event) => {
      if (event.target?.tagName === 'IMG') {
        replace(event.target);
      }
    };
    container.addEventListener('error', onError, true);
    // A load that already failed fires nothing further — the image reports itself
    // complete with no intrinsic width — so those are swapped now rather than waited for.
    container.querySelectorAll('img').forEach((img) => {
      if (img.complete && img.naturalWidth === 0) {
        replace(img);
      }
    });
    return () => container.removeEventListener('error', onError, true);
  }, [html, hasSourceLink]);

  return (
    <Box
      ref={containerRef}
      data-testid='kb-document-html'
      sx={{
        maxHeight: 360,
        overflow: 'auto',
        fontSize: 'var(--ds-text-body)',
        '& img': { maxWidth: '100%' },
        '& table': { borderCollapse: 'collapse', width: '100%' },
        '& td, & th': { border: `1px solid ${ds.gray[200]}`, padding: `${ds.space[1]} ${ds.space[2]}` },
        '& pre': { overflow: 'auto', background: ds.gray[100], padding: ds.space[2] },
      }}
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
};

StoredHtml.propTypes = {
  html: PropTypes.string.isRequired,
  hasSourceLink: PropTypes.bool,
};

const KBDocumentsModal = ({ open, onClose, accountId, kbId, kbName, canEdit, knowledgeBase }) => {
  const [documents, setDocuments] = useState([]);
  const [nextOffset, setNextOffset] = useState(null);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState(null);
  const [expandedId, setExpandedId] = useState(null);
  const [indexed, setIndexed] = useState(null);
  const [rebuilding, setRebuilding] = useState(false);
  // State lags a click by a render, so two fast clicks can both read reindexing as
  // false and fire two rebuilds. The ref is set in the same tick as the first.
  const reindexingRef = useRef(false);
  const [reindexing, setReindexing] = useState(false);
  const [contents, setContents] = useState({});
  // Marks are held by document_key rather than id so they survive paging and so
  // the optimistic value and the server's agree on one handle.
  const [categories, setCategories] = useState({});
  const [savingKeys, setSavingKeys] = useState([]);
  // Documents whose mark was saved here. On an integration's organisation
  // knowledge base that save replaced every account's own mark (#40605), so
  // their "accounts differ" count no longer applies.
  const [settledKeys, setSettledKeys] = useState([]);
  const [categoryError, setCategoryError] = useState(null);
  // Which rows have their detail view open, and on which tab. Rows are independent, as in any accordion.
  const [openDetails, setOpenDetails] = useState({});
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
    setOpenDetails({});
    setCategories({});
    setSavingKeys([]);
    setSettledKeys([]);
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
          setIndexed(response.data.indexed);
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
          return;
        }
        setSettledKeys((prev) => [...prev, documentKey]);
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
        [documentId]: response.data
          ? { content: response.data.content || '', contentType: response.data.content_type, title: response.data.title }
          : { error: errorMessage(response, 'Failed to load document content') },
      }));
    });
  };

  // The index can be dropped without the knowledge base row noticing, so offer the
  // rebuild here — where someone discovers the gap — rather than only in the menu.
  const handleReindex = async () => {
    // A Banner action has no disabled state, so re-entry is guarded here: a second
    // click mid-flight would drop and rebuild the collection twice over.
    if (reindexingRef.current) return;
    reindexingRef.current = true;
    const key = requestKey.current;
    setReindexing(true);
    try {
      const response = await apiKnowledgeBase.retriggerKB(accountId, kbId);
      // The modal is reused across knowledge bases, so a reply that lands after the
      // reader moved on would clear the new one's banner or show the old one's error.
      if (key !== requestKey.current) return;
      setReindexing(false);
      if (response?.errors?.length) {
        setError(errorMessage(response, 'Failed to start reindexing'));
        return;
      }
      // Not setIndexed(true): the rebuild has only been queued, and it takes minutes.
      // Dropping the warning with nothing in its place reads as "fixed" and leaves the
      // list and counts below it stale with no hint that they are.
      setIndexed(null);
      setRebuilding(true);
    } catch (err) {
      // The API layer returns its errors rather than throwing, so this is unreachable
      // today. It is here because the guard above is a ref: a throw that skipped the
      // release would not stall one click, it would disable the button for good.
      console.error('kb: retrigger threw', err);
      if (key !== requestKey.current) return;
      setReindexing(false);
      setError('Failed to start reindexing');
    } finally {
      // Runs on every path, including the early returns, so switching knowledge base
      // mid-flight cannot leave the guard stuck on either.
      reindexingRef.current = false;
    }
  };

  const toggleDetail = (documentId) =>
    setOpenDetails((current) => {
      const next = { ...current };
      if (documentId in next) {
        delete next[documentId];
      } else {
        next[documentId] = 'page';
      }
      return next;
    });
  const setDetailTab = (documentId, tab) => setOpenDetails((current) => ({ ...current, [documentId]: tab }));

  const renderContent = (documentId, sourceUrl) => {
    const state = contents[documentId];
    if (!state || state.loading) return <Loader />;
    if (state.error) return <Banner tone='critical' message={state.error} />;
    if (!state.content) {
      return <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>This document has no stored content.</Typography>;
    }
    // A scraped page is stored in the form the site returned it, so it arrives as
    // HTML and has to be rendered to be readable — as markup it is a wall of tags.
    // Sanitized rather than trusted: this is third-party content from someone's wiki.
    // Relative images and links point back at that site and will not resolve here.
    // An uploaded file is served from the database, so it still opens when the
    // index has been rebuilt or dropped — the file is the only copy we keep.
    if (state.contentType === 'application/pdf') {
      return <PdfDocument base64={state.content} title={state.title || 'document.pdf'} />;
    }
    if (state.contentType === 'application/json') {
      return <CodeBlock code={state.content} language='json' wrap maxHeight={360} />;
    }
    if (state.contentType === 'text/html') {
      return <StoredHtml html={sanitizeStoredHtml(state.content)} hasSourceLink={Boolean(sourceUrl)} />;
    }
    // Anything else was converted on the way in: a typed note, or a page the
    // indexer already turned into markdown.
    return (
      <Box sx={{ maxHeight: 360, overflow: 'auto' }} data-testid='kb-document-markdown'>
        <MarkDowns data={state.content} />
      </Box>
    );
  };

  return (
    <Modal open={open} handleClose={onClose} title={`Documents: ${kbName || ''}`} width='lg'>
      <Box sx={{ padding: ds.space[4] }} data-testid='kb-documents-modal'>
        {rebuilding && !loading && (
          <Box sx={{ mb: ds.space[3] }} data-testid='kb-rebuilding'>
            <Banner tone='info' message='Rebuilding the index. This takes a few minutes; reopen this window to see the result.' />
          </Box>
        )}
        {indexed === false && !rebuilding && !loading && (
          <Box sx={{ mb: ds.space[3] }} data-testid='kb-not-indexed'>
            <Banner
              tone='warning'
              message='Nothing is indexed, so the assistant cannot retrieve from this knowledge base. Its stored content still shows below.'
              actionsPlacement='inline'
              actions={[
                {
                  // The same operation as the three-dots menu, so it carries the same
                  // name — two words for one action invites "what is the difference?".
                  label: reindexing ? 'Retriggering…' : 'Retrigger',
                  onClick: handleReindex,
                },
              ]}
            />
          </Box>
        )}
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
                  <strong>Default</strong> — no mark. A page the classifier labelled SOP is read as a procedure; any other page, including one that is
                  not classified, is read as reference material.
                </li>
                <li>
                  <strong>Classifier</strong> — what the automatic classifier decided about a page: SOP or knowledge, its procedure shape and the
                  platform capabilities it mentions. A mark above overrides it. “Not classified” means no label is stored for the page yet.
                </li>
                <li>
                  <strong>Arrow</strong> — open a page's details: the capabilities it references, what the catalog says about them, and its knowledge
                  base.
                </li>
                {/* Read-only viewers still see marks as labels, so they get the legend
                    too — plus where the marks can be changed. */}
                {!canEdit && <li>Select an account to change how a document is marked.</li>}
              </Box>
            )}
            {categoryError && <Banner tone='critical' message={categoryError} />}
            {documents.map((doc) => {
              const expanded = expandedId === doc.id;
              const detailOpen = doc.id in openDetails;
              const documentKey = doc.document_key || '';
              const category = categories[documentKey] || '';
              const saving = savingKeys.includes(documentKey);
              const accountsDiffer = settledKeys.includes(documentKey) ? 0 : doc.account_marks || 0;
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
                      {/* A hand-made note is one document the classifier does not label per document, so
                          it would read "Not classified" wrongly; only pages with a key, or a label, show it. */}
                      {(documentKey || doc.kind) && <ClassifierLine doc={doc} />}
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
                    {accountsDiffer > 0 && (
                      <Label
                        text={`${accountsDiffer} ${accountsDiffer === 1 ? 'account differs' : 'accounts differ'}`}
                        tone='warning'
                        id={`kb-document-accounts-differ-${doc.id}`}
                      />
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
                    {/* The accordion arrow sits at the right end of the row. A hand-made note has nothing to
                        expand, so only pages with a key, or a label, get it. */}
                    {(documentKey || doc.kind) && (
                      <Button
                        tone='ghost'
                        size='sm'
                        composition='icon-only'
                        icon={detailOpen ? <KeyboardArrowUpIcon /> : <KeyboardArrowDownIcon />}
                        tooltip={detailOpen ? 'Hide details' : 'Show details'}
                        aria-label={`${detailOpen ? 'Hide' : 'Show'} details for ${doc.title}`}
                        aria-expanded={detailOpen}
                        data-testid='kb-document-details-toggle'
                        onClick={() => toggleDetail(doc.id)}
                      />
                    )}
                  </Box>
                  {detailOpen && (
                    <Box sx={{ mt: ds.space[2] }}>
                      <DocumentDetailBody
                        doc={doc}
                        mark={category}
                        tab={openDetails[doc.id]}
                        onTab={(tab) => setDetailTab(doc.id, tab)}
                        knowledgeBase={knowledgeBase}
                        documents={documents}
                      />
                    </Box>
                  )}
                  {expanded && <Box sx={{ mt: ds.space[2] }}>{renderContent(doc.id, doc.url)}</Box>}
                </Box>
              );
            })}
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
  // Whether the viewer may change a document's Fact/SOP mark. Read-only
  // viewers see the mark as a label instead of a control.
  canEdit: PropTypes.bool,
  // The knowledge base's own row (roll-up and the catalog details of its capabilities), as the Knowledge Base tab
  // already holds it. Optional: the detail view degrades to a hint.
  knowledgeBase: PropTypes.shape({
    kinds: PropTypes.arrayOf(PropTypes.string),
    shapes: PropTypes.arrayOf(PropTypes.string),
    capabilities: PropTypes.arrayOf(PropTypes.string),
    capability_details: PropTypes.arrayOf(PropTypes.object),
  }),
};

export default KBDocumentsModal;
