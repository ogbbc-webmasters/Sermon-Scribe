module Types exposing (Model, Msg(..), SermonList(..), UploadState(..))

import Api exposing (Sermon)
import Editor
import File exposing (File)
import Http
import Json.Decode as Decode
import Set exposing (Set)
import Time


type SermonList
    = Loading
    | Loaded (List Sermon)
    | LoadFailed


type UploadState
    = Idle
    | Uploading Float
    | UploadFailed String


type alias Model =
    { sermons : SermonList
    , editing : Maybe Editor.Model
    , selectedSermon : Maybe Sermon
    , transcriptSearch : String
    , transcriptMatch : Int
    , transcriptCopyStatus : Maybe Bool
    , hasPipelineSnapshot : Bool
    , upload : UploadState
    , confirmingDelete : Maybe Sermon
    , deleting : Set String
    , deletedSermons : Set String
    , deleteError : Maybe String
    , retrying : Set String
    , retryError : Maybe String
    , rerunning : Set String
    , reviewingNormalization : Set String
    , normalizationError : Maybe String
    , zone : Time.Zone
    }


type Msg
    = NoOp
    | GotZone Time.Zone
    | GotSermons (Result Http.Error (List Sermon))
    | PipelineEventReceived Decode.Value
    | FilePicked File
    | UploadProgress Http.Progress
    | UploadFinished (Result Http.Error Sermon)
    | RetrySermon Sermon
    | RetryProcessing Sermon String
    | RetryFinished Sermon (Result Http.Error Sermon)
    | RerunNormalization Sermon String
    | RerunNormalizationFinished Sermon (Result Http.Error Sermon)
    | ReviewNormalization Sermon
    | NormalizationReviewed Sermon (Result Http.Error Sermon)
    | OpenEditor Sermon
    | OpenSermon Sermon
    | CloseSermon
    | SearchTranscript String
    | SelectTranscriptMatch Int
    | CopyTranscript String
    | TranscriptCopied Bool
    | EditorMsg Editor.Msg
    | AskDelete Sermon
    | CancelDelete
    | ConfirmDelete Sermon
    | DeleteFinished String (Result Http.Error ())
