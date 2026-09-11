import unittest

from rag.core.documents.knowledge_tags import add_knowledge_context_tags
from rag.core.types import Document


class KnowledgeTagsTest(unittest.TestCase):
    def test_search_representation_preserves_source(self):
        source = Document(page_content="Restart only after approval.", metadata={"source": "manual"})
        indexed = add_knowledge_context_tags([source], [" payments ", "Payments", "service: checkout"])
        self.assertIn('Context tags: ["payments", "service: checkout"]', indexed[0].page_content)
        self.assertTrue(indexed[0].page_content.endswith(source.page_content))
        self.assertEqual(source.page_content, "Restart only after approval.")
        self.assertNotIn("context_tags", source.metadata)
        self.assertEqual(indexed[0].metadata["source"], "manual")

    def test_untagged_and_cleared_content_unchanged(self):
        docs = [Document(page_content="original")]
        self.assertIs(add_knowledge_context_tags(docs, []), docs)
        self.assertIs(add_knowledge_context_tags(docs, [" "]), docs)

    def test_each_format_document_gets_labels(self):
        docs = [Document(page_content="row1"), Document(page_content="row2")]
        indexed = add_knowledge_context_tags(docs, ["database"])
        self.assertEqual(len(indexed), 2)
        for doc in indexed:
            self.assertIn("database", doc.page_content)

    def test_invalid_labels(self):
        for labels in [["x"] * 33, ["x" * 129], ["line\nbreak"]]:
            with self.assertRaises(ValueError):
                add_knowledge_context_tags([], labels)
